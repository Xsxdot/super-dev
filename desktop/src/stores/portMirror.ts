/**
 * portMirrorStore 管理端口镜像状态的前端订阅缓存与事件流。
 *
 * 职责：
 *   - 启动时从 /api/port-mirrors 获取初始快照
 *   - 通过 /ws/port-mirrors 订阅 agent 侧 portmirror.Manager 的全量状态快照
 *   - 对比新旧快照 diff 出 MirrorEvent（established/conflict/failed/removed），
 *     供日志流镜像事件行消费，环形裁剪最近 200 条
 *   - 提供 retry / stopOccupier 两个动作的透传封装
 *
 * 边界：
 *   - 不做端口镜像的建立/拆除决策——那是 agent 侧 portmirror.Manager 的职责，
 *     本 store 只读它的快照、调它的 retry/stop-occupier 接口
 *   - events 只存于内存，不持久化，页面刷新即丢失（与日志面板的"实时视图"定位一致）
 */
import { ref } from 'vue'
import { defineStore } from 'pinia'
import { api, portMirrorWsUrl, type MirrorState, type MirrorStatus } from '@/api/agent'

const reconnectInitialDelayMs = 250
const reconnectMaxDelayMs = 5_000
// events 环形裁剪上限：只保留最近 200 条，避免长时间挂机的会话把内存吃满。
const eventsLimit = 200

/** MirrorEvent 是一条端口镜像状态跃迁的展示用事件，供日志流镜像事件行消费。 */
export interface MirrorEvent {
  deploymentId: string
  port: number
  hostName: string
  kind: 'established' | 'failed' | 'conflict' | 'removed'
  at: number // Date.now()
}

// mirrorEntryKey 是前端 diff 用的条目身份，对齐 agent 侧 portmirror.mirrorKey
// （host+deployment+port，见 agent/portmirror/manager.go）——同 host 同端口可能被两个
// deployment 同时声明，赢家与落败者是两条不同的状态条目（一 active、一 failed），
// 用 host_id+port 当 key 会把它们错误合并成一条，必须三元组一起寻址。
function mirrorEntryKey(m: MirrorStatus): string {
  return `${m.host_id}::${m.deployment_id}::${m.port}`
}

// stateToEventKind 把状态跃迁的终态映射为事件类型；pending 是「正在尝试建立」的
// 过渡态，没有对应的展示事件——用户不需要为它单独收到一条日志行。
function stateToEventKind(state: MirrorState): MirrorEvent['kind'] | undefined {
  if (state === 'active') return 'established'
  if (state === 'conflict') return 'conflict'
  if (state === 'failed') return 'failed'
  return undefined
}

function toEvent(m: MirrorStatus, kind: MirrorEvent['kind']): MirrorEvent {
  return { deploymentId: m.deployment_id, port: m.port, hostName: m.host_name, kind, at: Date.now() }
}

// diffSnapshots 对比新旧全量快照，派生本轮变化对应的 MirrorEvent。
//
// 参数：
//   - prev/next: 分别是上一次、这一次收到的全量快照
//
// 返回：
//   - 本轮变化产出的事件列表（可能为空）
//
// 注意：
//   - 为什么用快照 diff 而不是后端推事件：/ws/port-mirrors 是「满则丢」的全量流
//     （channel 缓冲区满时丢最旧帧，消费方以最新快照为准，见 Manager.Subscribe 注释）。
//     如果后端改为推「事件」，一旦丢帧就会永久漏发，前端永远不知道发生过什么；
//     而全量快照 diff 只依赖"当前"与"上一次成功收到"这两份快照本身的差异，
//     中间实际丢了多少帧完全不影响 diff 结果的正确性——对丢帧天然免疫。
//   - 状态未变的条目不产出事件；新增条目直接以 pending 出现时也不产出事件
//     （pending 没有对应 kind，见 stateToEventKind）
function diffSnapshots(prev: MirrorStatus[], next: MirrorStatus[]): MirrorEvent[] {
  const events: MirrorEvent[] = []
  const prevByKey = new Map(prev.map(m => [mirrorEntryKey(m), m]))
  const nextByKey = new Map(next.map(m => [mirrorEntryKey(m), m]))

  for (const [key, entry] of nextByKey) {
    const before = prevByKey.get(key)
    if (before && before.state === entry.state) continue
    const kind = stateToEventKind(entry.state)
    if (kind) events.push(toEvent(entry, kind))
  }
  for (const [key, entry] of prevByKey) {
    if (!nextByKey.has(key)) events.push(toEvent(entry, 'removed'))
  }
  return events
}

export const usePortMirrorStore = defineStore('portMirror', () => {
  const mirrors = ref<MirrorStatus[]>([])
  const events = ref<MirrorEvent[]>([])
  const connected = ref(false)
  const error = ref<string | null>(null)
  let ws: WebSocket | null = null
  let starting: Promise<void> | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectDelayMs = reconnectInitialDelayMs
  let activeConsumers = 0

  function applySnapshot(snapshot: MirrorStatus[]) {
    const newEvents = diffSnapshots(mirrors.value, snapshot)
    if (newEvents.length) {
      events.value = [...events.value, ...newEvents].slice(-eventsLimit)
    }
    mirrors.value = snapshot
  }

  async function loadSnapshot() {
    // 初次拉取是基线水位，直接赋值、不经 applySnapshot 的 diff——否则启动时一整批
    // 既有 active 镜像会被误判成"刚刚建立"，刷出一屏幕并不新鲜的假事件。
    // 代价（有意接受）：断连窗口内发生的跃迁（conflict/removed 等）不会补发事件——
    // 重连即重置基线，gap 内的变化只体现在最新状态里，不出现在日志流事件行。
    mirrors.value = await api.listPortMirrors()
  }

  async function start() {
    activeConsumers += 1
    if (ws) return
    if (starting) return starting
    starting = startInner().finally(() => {
      starting = null
    })
    return starting
  }

  async function startInner() {
    clearReconnectTimer()
    try {
      await loadSnapshot()
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load port mirror snapshot'
    }
    if (activeConsumers <= 0 || ws) return
    const url = await portMirrorWsUrl()
    // portMirrorWsUrl 是 async（内部要经 Tauri IPC 读取本机 token 再拼 access_token）。
    // 这段 await 期间 stop() 完全可能被调用，因此拿到 url 后必须重新校验一次状态，
    // 否则会建立一条没人持有引用、永远不会被关闭的 WebSocket（连接泄漏）。
    if (activeConsumers <= 0 || ws) return
    ws = new WebSocket(url)
    ws.onopen = () => {
      connected.value = true
      error.value = null
      reconnectDelayMs = reconnectInitialDelayMs
    }
    ws.onmessage = event => {
      try {
        applySnapshot(JSON.parse(event.data) as MirrorStatus[])
      } catch {
        // 忽略损坏帧，避免单条异常影响整条状态线。
      }
    }
    ws.onerror = () => {
      error.value = 'Port mirror status stream error'
    }
    ws.onclose = () => {
      connected.value = false
      ws = null
      scheduleReconnect()
    }
  }

  function stop() {
    if (activeConsumers > 0) activeConsumers -= 1
    if (activeConsumers > 0) return
    clearReconnectTimer()
    ws?.close()
    ws = null
    connected.value = false
  }

  function scheduleReconnect() {
    if (activeConsumers <= 0 || ws || starting || reconnectTimer) return
    const delay = reconnectDelayMs
    reconnectDelayMs = Math.min(reconnectDelayMs * 2, reconnectMaxDelayMs)
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      if (activeConsumers <= 0 || ws || starting) return
      starting = startInner().finally(() => {
        starting = null
      })
    }, delay)
  }

  function clearReconnectTimer() {
    if (!reconnectTimer) return
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }

  function mirrorsForDeployment(depId: string): MirrorStatus[] {
    return mirrors.value.filter(m => m.deployment_id === depId)
  }

  function mirrorsForHost(hostId: string): MirrorStatus[] {
    return mirrors.value.filter(m => m.host_id === hostId)
  }

  /** retry 清除指定 host+port 的冷却记忆并请求立即重试；结果通过状态流回传，不在此处乐观更新。 */
  async function retry(hostId: string, port: number) {
    await api.retryPortMirror(hostId, port)
  }

  /** stopOccupier 请求停止占用该 host+port 的进程（托管走 stop_service，非托管发 SIGTERM）。 */
  async function stopOccupier(hostId: string, port: number) {
    await api.stopMirrorOccupier(hostId, port)
  }

  return {
    mirrors,
    events,
    connected,
    error,
    start,
    stop,
    applySnapshot,
    mirrorsForDeployment,
    mirrorsForHost,
    retry,
    stopOccupier,
  }
})
