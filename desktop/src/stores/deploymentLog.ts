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
const TRIMMED_FOLD_KEYS_MAX = 512
const MAX_RECONNECT = 5
const REPLAY_ON_RECONNECT = 100
const CATCHUP_PAGE_LIMIT = 200
// 补拉最多翻 20 页（4000 条）。再多说明断线太久，剩余部分用缺口标记明示，
// 不无限追赶（用户此刻要看的是最新日志，陈年区间可通过向上翻历史补齐）。
const CATCHUP_MAX_PAGES = 20

interface LogCursor {
  time: string
  id: string
}

interface DeploymentSession {
  refCount: number
  ws: WebSocket | null
  projectId: string | null
  logs: DisplayLogEntry[]
  hasMoreHistory: boolean
  oldestCursor: LogCursor | null
  loadingMoreHistory: boolean
  lastSeen: LogCursor | null
  catchingUp: boolean
  pendingLive: LogEntry[]
  gapMarkers: { id: string; time: string }[]
  reconnectAttempts: number
  discontinuous: boolean
  liveStartIndex: number
  trimmedFoldKeys: Map<string, number>
  liveIdState: Map<string, { active: number; nextOrdinal: number }>
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
      projectId: null,
      logs: [],
      hasMoreHistory: true,
      oldestCursor: null,
      loadingMoreHistory: false,
      lastSeen: null,
      catchingUp: false,
      pendingLive: [],
      gapMarkers: [],
      reconnectAttempts: 0,
      discontinuous: false,
      liveStartIndex: 0,
      trimmedFoldKeys: new Map(),
      liveIdState: new Map(),
    }
  }

  function cursorFromLog(log: LogEntry): LogCursor {
    // cursorFromLog 构造翻页/补拉游标。后端游标已切到 per-deployment seq（期 2a），
    // 优先取 seq；无 seq（旧 agent 数据）回退 rowid——两者同序，退化只影响精度不影响正确性。
    const seq = log.seq && log.seq > 0 ? String(log.seq) : ''
    return { time: log.timestamp, id: seq || String(log.id ?? '') }
  }

  function gapMarkerID(): string {
    return globalThis.crypto?.randomUUID?.() ?? `gap-${Date.now()}-${Math.random()}`
  }

  // diagnostic 通过 superdev:log-panel 事件打点，非 console.log，便于 tail_logs 复盘。
  function diagnostic(event: string, ctx: Record<string, unknown>) {
    if (typeof window === 'undefined') return
    window.dispatchEvent(new CustomEvent('superdev:log-panel', {
      detail: { scope: 'log-store', level: 'debug', event, at: new Date().toISOString(), ...ctx },
    }))
  }

  function liveBaseId(id: string): string | null {
    if (!id.startsWith('live-')) return null
    return id.replace(/#\d+$/, '')
  }

  function rawHasDatabaseId(raw: LogEntry): boolean {
    const rawID = String(raw.id ?? '')
    return rawID !== '' && rawID !== '0'
  }

  // ensureUniqueLiveId 在同一活跃缓冲内给无 rowid 实时日志消歧，避免相同 payload 互相覆盖。
  function ensureUniqueLiveId(session: DeploymentSession, raw: LogEntry, entry: DisplayLogEntry) {
    if (rawHasDatabaseId(raw)) return
    const baseId = entry.id
    const state = session.liveIdState.get(baseId) ?? { active: 0, nextOrdinal: 1 }
    const ordinal = state.nextOrdinal
    state.active++
    state.nextOrdinal++
    session.liveIdState.set(baseId, state)
    if (ordinal <= 1) return
    entry.id = `${baseId}#${ordinal}`
    diagnostic('log_store.live_id_collision', {
      deploymentId: raw.deployment_id,
      baseId,
      ordinal,
    })
  }

  function releaseLiveId(session: DeploymentSession, entry: DisplayLogEntry) {
    const baseId = liveBaseId(entry.id)
    if (!baseId) return
    const state = session.liveIdState.get(baseId)
    if (!state) return
    state.active--
    if (state.active <= 0) session.liveIdState.delete(baseId)
  }

  // trimHead 从头部裁掉 count 条，并把 oldestCursor 同步前移到裁剪边界。
  //
  // 为什么必须同步游标：裁剪只删前端缓冲，store 里数据还在。若 oldestCursor
  // 停在旧锚点，loadMoreHistory 会跳过 (旧锚点..裁剪边界) 直接翻更早的日志，
  // 该区间成为永久盲区——这就是"面板找不到、MCP 搜得到"的根因。
  function trimHead(session: DeploymentSession, count: number) {
    const removable = Math.min(count, session.logs.length)
    if (removable <= 0) return 0
    const removed = session.logs.splice(0, removable)
    for (const entry of removed) {
      if (entry.fold_key) {
        session.trimmedFoldKeys.set(entry.fold_key, entry.repeat_count)
        // fold_key 记录仅用于区分"预期 miss"，容量收敛到最近 512 个，防内存无限涨。
        if (session.trimmedFoldKeys.size > TRIMMED_FOLD_KEYS_MAX) {
          const oldest = session.trimmedFoldKeys.keys().next().value
          if (oldest !== undefined) session.trimmedFoldKeys.delete(oldest)
        }
      }
      releaseLiveId(session, entry)
    }
    const boundary = removed[removed.length - 1]
    if (boundary) {
      // 边界条目可能是无 rowid 的实时日志（cursor_id 为空），此时退化为纯时间游标，
      // 由后端 before_time 参数兜底（Task 1）。
      session.oldestCursor = { time: boundary.timestamp, id: boundary.cursor_id ?? '' }
      // 裁掉了缓冲就意味着"又有历史可翻了"，必须重置，否则翻页永久停摆。
      session.hasMoreHistory = true
    }
    session.liveStartIndex = Math.max(0, session.liveStartIndex - removed.length)
    return removed.length
  }

  // trimHistoryHead 只裁历史区头部，并保留被裁 fold_key 的最后计数。
  function trimHistoryHead(session: DeploymentSession, count: number) {
    const removable = Math.min(count, session.liveStartIndex)
    return trimHead(session, removable)
  }

  // trimIfNeeded 先裁历史头部；历史不足时再裁实时头部，保证 live-only 会话也有内存上限。
  function trimIfNeeded(session: DeploymentSession) {
    if (session.logs.length <= MAX_LOGS) return
    const target = Math.max(TRIM_BATCH, session.logs.length - MAX_LOGS)
    const historyRemoved = trimHistoryHead(session, target)
    let liveRemoved = 0
    if (session.logs.length > MAX_LOGS) {
      liveRemoved = trimHead(session, Math.max(TRIM_BATCH, session.logs.length - MAX_LOGS))
    }
    diagnostic('log_store.trim', {
      historyRemoved,
      liveRemoved,
      kept: session.logs.length,
      liveStartIndex: session.liveStartIndex,
      oldestCursorTime: session.oldestCursor?.time,
      oldestCursorId: session.oldestCursor?.id,
    })
  }

  /**
   * ingestHistoryBatch 将一页历史日志批量插入历史前缀，实时后缀保持原位。
   *
   * 注意：
   *   - 批量页只替换历史前缀一次，避免大页历史逐条 slice/splice 导致 O(n²) 卡顿
   *   - 历史区严格按时间戳有序，用于向上翻页和 anchor 复位
   *   - 不把已有实时日志重新归入历史区，避免分区边界乱跳
   */
  function ingestHistoryBatch(session: DeploymentSession, raws: LogEntry[]): DisplayLogEntry[] {
    if (raws.length === 0) return []
    const entries = raws.map(toDisplayEntry)
    const history = session.logs.slice(0, session.liveStartIndex)
    // API 可能返回新到旧或旧到新；统一经 insertSorted 合并，确保同 id 替换且顺序稳定。
    for (const entry of entries) {
      insertSorted(history, entry)
    }
    session.logs.splice(0, session.liveStartIndex, ...history)
    session.liveStartIndex = history.length
    trimIfNeeded(session)
    bumpRevision()
    touchSessions()
    diagnostic('log_store.history_batch_ingest', {
      added: raws.length,
      kept: session.logs.length,
      liveStartIndex: session.liveStartIndex,
    })
    return entries
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
    ensureUniqueLiveId(session, raw, entry)
    appendLive(session.logs, entry, session.liveStartIndex)
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
        if (s.catchingUp) {
          // 补拉进行中：实时日志先缓冲，避免与补拉页交叉乱序。
          // 上限防补拉卡死时内存爆掉；超限丢弃由 catch-up 结束后的 gap 标记兜底。
          if (s.pendingLive.length < 2000) s.pendingLive.push(raw)
          return
        }
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
    ws.onopen = () => {
      const s = sessions.value.get(deploymentId)
      if (!s || s.ws !== ws) return
      if (s.lastSeen && s.projectId) {
        void catchUpFromLastSeen(deploymentId, s)
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

  // catchUpFromLastSeen 断线重连后从 lastSeen 游标向后补拉，直到追上或达页数上限。
  //
  // 为什么不用 WS 的 replay 参数：replay 固定回放最近 N 条，断线超过 N 条就留洞
  // 且永远补不回来；按游标补拉是确定性的。replay 参数保留（兜首连与无 projectId 场景），
  // 补拉页与 replay 的重复条目由同 id 覆盖去重兜底。
  async function catchUpFromLastSeen(deploymentId: string, session: DeploymentSession) {
    session.catchingUp = true
    touchSessions()
    let pages = 0
    let cursor = session.lastSeen!
    let hasMore = true
    try {
      while (hasMore && pages < CATCHUP_MAX_PAGES) {
        pages++
        const resp = await api.fetchLogContextPage({
          project: session.projectId!,
          deployment: deploymentId,
          cursor_time: cursor.time,
          cursor_id: cursor.id,
          direction: 'after',
          limit: CATCHUP_PAGE_LIMIT,
        })
        const items = resp.items ?? []
        for (const raw of items) {
          ingestLive(session, raw)
        }
        if (items.length > 0) {
          cursor = cursorFromLog(items[items.length - 1])
          session.lastSeen = cursor
        }
        hasMore = resp.has_more === true && items.length > 0
      }
      if (hasMore) {
        // 补拉封顶仍没追上：明示缺口，宁可让用户看到"这里缺了"，不静默装完整。
        session.gapMarkers.push({ id: gapMarkerID(), time: cursor.time })
        diagnostic('log_store.catchup_capped', { deploymentId, pages, cursorTime: cursor.time })
      }
      diagnostic('log_store.catchup_done', { deploymentId, pages, caughtUp: !hasMore })
    } catch (err) {
      // 补拉失败同样明示缺口——诊断事件 + gap 标记双通道，不静默。
      session.gapMarkers.push({ id: gapMarkerID(), time: cursor.time })
      diagnostic('log_store.catchup_failed', {
        deploymentId,
        message: err instanceof Error ? err.message : String(err),
      })
    } finally {
      session.catchingUp = false
      const pending = session.pendingLive
      session.pendingLive = []
      for (const raw of pending) {
        if (raw.message === '' && raw.fold_key) {
          applyIncrement(session, raw)
        } else {
          ingestLive(session, raw)
          session.lastSeen = cursorFromLog(raw)
        }
      }
      bumpRevision()
      touchSessions()
    }
  }

  /**
   * subscribe 订阅指定 deployment 的实时日志流。
   *
   * 参数：
   *   - deploymentId: deployment 唯一标识
   *   - projectId: deployment 所属项目，用于重连后按上下文 API 精确补拉
   *
   * 注意：
   *   - 相同 deploymentId 多次调用只建立一个 WebSocket，refCount 累加
   *   - 必须与 unsubscribe 配对使用，否则 WebSocket 不会关闭
   */
  function subscribe(deploymentId: string, projectId?: string | null) {
    const existing = sessions.value.get(deploymentId)
    if (existing) {
      existing.refCount++
      if (!existing.projectId && projectId) existing.projectId = projectId
      touchSessions()
      return
    }

    const session = makeSession()
    session.projectId = projectId ?? null
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
        before: session.oldestCursor?.id || undefined,
        beforeTime: session.oldestCursor?.time || undefined,
      })
      const entries = page.items
      const displayEntries = ingestHistoryBatch(session, entries)
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

  /** getGapMarkers 返回该 deployment 的断流缺口标记（时间点），供面板渲染缺口分隔行。 */
  function getGapMarkers(deploymentId: string): { id: string; time: string }[] {
    return sessions.value.get(deploymentId)?.gapMarkers ?? []
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
    getGapMarkers,
    refCountOf,
  }
})
