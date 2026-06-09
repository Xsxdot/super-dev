// deploymentLog store 管理 deployment 实时日志流（WebSocket）和历史日志。
//
// 职责：
//   - 按 deploymentId 管理 WebSocket 连接（refCount 支持多面板共享）
//   - 将收到的日志插入有序缓冲，供 LogPanel 消费
//   - 支持拉取历史日志（/api/deployments/{id}/logs）
//
// 边界：
//   - 不渲染 UI
//   - 不处理过滤、高亮、书签，交给 Panel 层
import { defineStore } from 'pinia'
import { markRaw, ref } from 'vue'
import { api, deploymentWsUrl, type LogEntry } from '@/api/agent'
import { applyEvent, toDisplayEntry, type DisplayLogEntry } from '@/lib/logEngine'
import { insertSorted } from '@/lib/logOrder'

const MAX_LOGS = 5000
const TRIM_BATCH = 500
const MAX_RECONNECT = 5
const REPLAY_ON_RECONNECT = 100

interface LogCursor {
  time: string
  id: string
}

interface DeploymentSession {
  refCount: number
  ws: WebSocket | null
  logs: DisplayLogEntry[]
  hasMoreHistory: boolean
  oldestCursor: LogCursor | null
  loadingMoreHistory: boolean
  lastSeen: LogCursor | null
  reconnectAttempts: number
  discontinuous: boolean
}

export interface LoadHistoryResult {
  added: number
  entries: DisplayLogEntry[]
}

export const useDeploymentLogStore = defineStore('deploymentLog', () => {
  const sessions = ref<Map<string, DeploymentSession>>(new Map())
  const logSourceRevision = ref(0)

  function touchSessions() {
    sessions.value = new Map(sessions.value)
  }

  function bumpRevision() {
    logSourceRevision.value++
  }

  function makeSession(): DeploymentSession {
    return {
      refCount: 1,
      ws: null,
      logs: [],
      hasMoreHistory: true,
      oldestCursor: null,
      loadingMoreHistory: false,
      lastSeen: null,
      reconnectAttempts: 0,
      discontinuous: false,
    }
  }

  function cursorFromLog(log: LogEntry): LogCursor {
    return { time: log.timestamp, id: String(log.id ?? '') }
  }

  /**
   * ingestEntry 将原始日志条目转换并按时间顺序插入 session 的日志缓冲。
   *
   * 注意：
   *   - 使用 insertSorted 二分插入，避免每次全量重建 Map + 全排序（O(n log n)）
   *   - 使用 id 做去重，避免 WebSocket 重连导致重复消息
   *   - 同 id 的日志以新内容覆盖旧内容
   *   - 超出 MAX_LOGS 时裁剪最早的条目，防止内存无限增长
   */
  function ingestEntry(session: DeploymentSession, raw: LogEntry): DisplayLogEntry {
    const entry = toDisplayEntry(raw)
    insertSorted(session.logs, entry)
    if (session.logs.length > MAX_LOGS) {
      session.logs.splice(0, TRIM_BATCH)
    }
    bumpRevision()
    touchSessions()
    return entry
  }

  function applyIncrement(session: DeploymentSession, raw: LogEntry) {
    applyEvent(session.logs, {
      increment: {
        deployment_id: raw.deployment_id,
        fold_key: raw.fold_key ?? '',
        repeat_count: raw.repeat_count ?? 1,
      },
    })
    bumpRevision()
    touchSessions()
  }

  function connect(deploymentId: string, session: DeploymentSession) {
    const ws = markRaw(new WebSocket(deploymentWsUrl(deploymentId, {
      replay: session.lastSeen ? REPLAY_ON_RECONNECT : undefined,
      sinceTime: session.lastSeen?.time,
      sinceId: session.lastSeen?.id,
    })))
    session.ws = ws

    ws.onmessage = event => {
      try {
        const raw = JSON.parse(event.data) as LogEntry
        const s = sessions.value.get(deploymentId)
        if (!s || s.ws !== ws) return
        if (raw.message === '' && raw.fold_key) {
          applyIncrement(s, raw)
        } else {
          ingestEntry(s, raw)
          s.lastSeen = cursorFromLog(raw)
        }
        s.reconnectAttempts = 0
        s.discontinuous = false
      } catch {
        // 忽略解析失败的消息，避免单条损坏数据影响整体
      }
    }
    ws.onerror = () => {
      touchSessions()
    }
    ws.onclose = () => {
      const s = sessions.value.get(deploymentId)
      if (!s || s.ws !== ws) return
      s.ws = null
      if (s.refCount > 0 && s.reconnectAttempts < MAX_RECONNECT) {
        s.reconnectAttempts++
        const attempt = s.reconnectAttempts
        setTimeout(() => {
          const latest = sessions.value.get(deploymentId)
          if (latest && latest.refCount > 0 && latest.ws === null) {
            connect(deploymentId, latest)
          }
        }, 1000 * attempt)
      } else if (s.reconnectAttempts >= MAX_RECONNECT) {
        s.discontinuous = true
      }
      touchSessions()
    }
  }

  /**
   * subscribe 订阅指定 deployment 的实时日志流。
   *
   * 参数：
   *   - deploymentId: deployment 唯一标识
   *
   * 注意：
   *   - 相同 deploymentId 多次调用只建立一个 WebSocket，refCount 累加
   *   - 必须与 unsubscribe 配对使用，否则 WebSocket 不会关闭
   */
  function subscribe(deploymentId: string) {
    const existing = sessions.value.get(deploymentId)
    if (existing) {
      existing.refCount++
      touchSessions()
      return
    }

    const session = makeSession()
    sessions.value.set(deploymentId, session)
    touchSessions()
    connect(deploymentId, session)
  }

  /**
   * unsubscribe 取消订阅指定 deployment 的日志流。
   *
   * 参数：
   *   - deploymentId: deployment 唯一标识
   *
   * 注意：
   *   - refCount 归零时才真正关闭 WebSocket 并清理 session
   *   - 多次调用 unsubscribe 超过订阅次数时 refCount 最低降到 0，不会负数
   */
  function unsubscribe(deploymentId: string) {
    const session = sessions.value.get(deploymentId)
    if (!session) return
    session.refCount = Math.max(0, session.refCount - 1)
    if (session.refCount > 0) {
      touchSessions()
      return
    }
    session.ws?.close()
    sessions.value.delete(deploymentId)
    touchSessions()
  }

  /**
   * loadMoreHistory 拉取更早的历史日志并合并到缓冲区。
   *
   * 参数：
   *   - deploymentId: deployment 唯一标识
   *   - limit: 每次拉取条数，默认 200
   *
   * 注意：
   *   - 若 hasMoreHistory 为 false 或正在加载则直接返回
   *   - 加载失败时静默忽略，上层可重试
   */
  async function loadMoreHistory(deploymentId: string, limit = 200): Promise<LoadHistoryResult> {
    const session = sessions.value.get(deploymentId)
    if (!session || !session.hasMoreHistory || session.loadingMoreHistory) {
      return { added: 0, entries: [] }
    }
    session.loadingMoreHistory = true
    touchSessions()
    try {
      const page = await api.fetchDeploymentLogs({
        deploymentId,
        limit,
        before: session.oldestCursor?.id,
      })
      const entries = page.items
      const displayEntries = entries.map(toDisplayEntry)
      // 倒序插入：最新的先入，insertSorted 追加到末尾性能最优
      for (let i = entries.length - 1; i >= 0; i--) {
        ingestEntry(session, entries[i])
      }
      if (page.next?.id) {
        session.oldestCursor = { time: page.next.time ?? entries[0]?.timestamp ?? '', id: page.next.id }
      } else if (entries.length > 0) {
        session.oldestCursor = cursorFromLog(entries[0])
      }
      session.hasMoreHistory = entries.length >= limit
      return { added: entries.length, entries: displayEntries }
    } catch (err) {
      console.error('Failed to load deployment log history', err)
      return { added: 0, entries: [] }
    } finally {
      session.loadingMoreHistory = false
      touchSessions()
    }
  }

  /**
   * getLogs 返回指定 deployment 的已排序日志列表。
   *
   * 参数：
   *   - deploymentId: deployment 唯一标识
   *
   * 返回：
   *   - DisplayLogEntry 列表，未订阅时返回空数组
   */
  function getLogs(deploymentId: string): DisplayLogEntry[] {
    return sessions.value.get(deploymentId)?.logs ?? []
  }

  /**
   * hasMoreHistory 返回指定 deployment 是否还有更早的历史可加载。
   */
  function hasMoreHistory(deploymentId: string): boolean {
    return sessions.value.get(deploymentId)?.hasMoreHistory ?? false
  }

  /**
   * isLoadingMore 返回指定 deployment 是否正在加载历史日志。
   */
  function isLoadingMore(deploymentId: string): boolean {
    return sessions.value.get(deploymentId)?.loadingMoreHistory ?? false
  }

  /**
   * refCountOf 返回指定 deployment 的当前引用计数。
   *
   * 返回：
   *   - 引用计数，未订阅时返回 0
   */
  function refCountOf(deploymentId: string): number {
    return sessions.value.get(deploymentId)?.refCount ?? 0
  }

  return {
    sessions,
    logSourceRevision,
    subscribe,
    unsubscribe,
    loadMoreHistory,
    getLogs,
    hasMoreHistory,
    isLoadingMore,
    refCountOf,
  }
})
