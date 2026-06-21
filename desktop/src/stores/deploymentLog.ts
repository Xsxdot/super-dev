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
import { appendLive, insertSorted } from '@/lib/logOrder'

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
  liveStartIndex: number
  trimmedFoldKeys: Map<string, number>
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
      liveStartIndex: 0,
      trimmedFoldKeys: new Map(),
    }
  }

  function cursorFromLog(log: LogEntry): LogCursor {
    return { time: log.timestamp, id: String(log.id ?? '') }
  }

  // diagnostic 通过 superdev:log-panel 事件打点，非 console.log，便于 tail_logs 复盘。
  function diagnostic(event: string, ctx: Record<string, unknown>) {
    if (typeof window === 'undefined') return
    window.dispatchEvent(new CustomEvent('superdev:log-panel', {
      detail: { scope: 'log-store', level: 'debug', event, at: new Date().toISOString(), ...ctx },
    }))
  }

  // trimHistoryHead 只裁历史区头部，并保留被裁 fold_key 的最后计数。
  function trimHistoryHead(session: DeploymentSession, count: number) {
    const removable = Math.min(count, session.liveStartIndex)
    if (removable <= 0) return 0
    const removed = session.logs.splice(0, removable)
    for (const entry of removed) {
      if (entry.fold_key) session.trimmedFoldKeys.set(entry.fold_key, entry.repeat_count)
    }
    session.liveStartIndex = Math.max(0, session.liveStartIndex - removed.length)
    return removed.length
  }

  // trimIfNeeded 超出 MAX_LOGS 时只裁历史区头部，避免实时尾部被裁导致“最新日志被吞”。
  function trimIfNeeded(session: DeploymentSession) {
    if (session.logs.length <= MAX_LOGS) return
    const removed = trimHistoryHead(session, TRIM_BATCH)
    diagnostic('log_store.trim', { removed, kept: session.logs.length, liveStartIndex: session.liveStartIndex })
  }

  /**
   * ingestHistory 将历史日志插入历史前缀，实时后缀保持原位。
   *
   * 注意：
   *   - 历史区严格按时间戳有序，用于向上翻页和 anchor 复位
   *   - 不把已有实时日志重新归入历史区，避免分区边界乱跳
   */
  function ingestHistory(session: DeploymentSession, raw: LogEntry): DisplayLogEntry {
    const entry = toDisplayEntry(raw)
    const history = session.logs.slice(0, session.liveStartIndex)
    insertSorted(history, entry)
    session.logs.splice(0, session.liveStartIndex, ...history)
    session.liveStartIndex = history.length
    trimIfNeeded(session)
    bumpRevision()
    touchSessions()
    return entry
  }

  /**
   * ingestLive 将实时日志追加进实时后缀。
   *
   * 注意：
   *   - 仅在实时区尾部做有限乱序纠正，超窗迟到日志不回插到可视区上方
   *   - 历史前缀不参与 appendLive，避免历史/实时互相重排
   */
  function ingestLive(session: DeploymentSession, raw: LogEntry): DisplayLogEntry {
    const entry = toDisplayEntry(raw)
    const live = session.logs.slice(session.liveStartIndex)
    appendLive(live, entry)
    session.logs.splice(session.liveStartIndex, session.logs.length - session.liveStartIndex, ...live)
    trimIfNeeded(session)
    bumpRevision()
    touchSessions()
    return entry
  }

  function applyIncrement(session: DeploymentSession, raw: LogEntry) {
    const foldKey = raw.fold_key ?? ''
    const hit = applyEvent(session.logs, {
      increment: {
        deployment_id: raw.deployment_id,
        fold_key: foldKey,
        repeat_count: raw.repeat_count ?? 1,
      },
    })
    // miss 分两种：被裁剪的折叠行是预期；其他 miss 需要 diagnostic 辅助定位乱序/丢包。
    if (!hit && !session.trimmedFoldKeys.has(foldKey)) {
      diagnostic('log_store.increment_miss', { deploymentId: raw.deployment_id, foldKey })
    }
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
          ingestLive(s, raw)
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
      // 倒序插入：最新的先入，insertSorted 追加到末尾性能最优
      for (let i = entries.length - 1; i >= 0; i--) {
        ingestHistory(session, entries[i])
      }
      const displayEntries = entries.map(toDisplayEntry)
      if (page.next?.id) {
        session.oldestCursor = { time: page.next.time ?? entries[0]?.timestamp ?? '', id: page.next.id }
      } else if (entries.length > 0) {
        session.oldestCursor = cursorFromLog(entries[0])
      }
      session.hasMoreHistory = entries.length >= limit
      return { added: entries.length, entries: displayEntries }
    } catch (err) {
      diagnostic('log_store.history_load_failed', {
        deploymentId,
        message: err instanceof Error ? err.message : String(err),
      })
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
    _ingestLive: (id: string, raw: LogEntry) => {
      const session = sessions.value.get(id)
      if (session) ingestLive(session, raw)
    },
    _ingestHistory: (id: string, raw: LogEntry) => {
      const session = sessions.value.get(id)
      if (session) ingestHistory(session, raw)
    },
    // _seedForTest 直接塞一条带 fold_key 的历史行，免去构造完整 LogEntry。
    _seedForTest: (id: string, { foldKey, repeatCount }: { foldKey: string; repeatCount: number }) => {
      const session = sessions.value.get(id)
      if (!session) return
      session.logs.unshift({
        id: `seed-${foldKey}`,
        deployment_id: id,
        run_id: '',
        timestamp: '2026-06-21T00:00:00Z',
        level: 'INFO',
        message: 'seed',
        stream: 'stdout',
        repeat_count: repeatCount,
        fold_key: foldKey,
      } as DisplayLogEntry)
      session.liveStartIndex++
    },
    _trimHistoryHead: (id: string, count: number) => {
      const session = sessions.value.get(id)
      if (!session) return
      trimHistoryHead(session, count)
    },
    _trimmedFoldKeysForTest: (id: string) => sessions.value.get(id)?.trimmedFoldKeys ?? new Map<string, number>(),
  }
})
