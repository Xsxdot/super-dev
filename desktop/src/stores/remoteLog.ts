// remoteLog store 管理某个远程 LogSource 分组的多节点实时日志和历史日志。
//
// 职责：
//   - 解析 LogSource 分组对应的 Host 集合
//   - 为每个 Host 打开 tunnel 并建立独立 WebSocket
//   - 将多 Host 日志按 timestamp 归并到一个有序列表
//   - 支持按 Host tunnel fan-out 拉取历史日志
//
// 边界：
//   - 不渲染 UI
//   - 不处理过滤、高亮和书签，交给 Panel 层已有能力
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  api,
  type LogEntry,
  type RemoteLogEntry,
  type RemoteViewResponse,
} from '@/api/agent'

interface GroupSession {
  refCount: number
  view: RemoteViewResponse | null
  logs: RemoteLogEntry[]
  sockets: Map<string, WebSocket>
  errors: Map<string, string>
  loadingHistory: boolean
  ports: Map<string, number>
  oldestIds: Map<string, number>
  hasMoreHistoryByHost: Map<string, boolean>
}

function sessionKey(logSourceId: string, groupKey: string): string {
  return `${logSourceId}::${groupKey}`
}

function compareLogs(a: RemoteLogEntry, b: RemoteLogEntry): number {
  const timeDiff = new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
  if (timeDiff !== 0) return timeDiff
  const idDiff = a.id - b.id
  if (idDiff !== 0) return idDiff
  return a.host_id.localeCompare(b.host_id)
}

function insertSorted(logs: RemoteLogEntry[], entry: RemoteLogEntry) {
  const existing = logs.findIndex(item => item.host_id === entry.host_id && item.id === entry.id)
  if (existing >= 0) {
    logs[existing] = entry
    logs.sort(compareLogs)
    return
  }
  if (logs.length === 0 || compareLogs(logs[logs.length - 1], entry) <= 0) {
    logs.push(entry)
    return
  }

  let lo = 0
  let hi = logs.length
  while (lo < hi) {
    const mid = (lo + hi) >>> 1
    if (compareLogs(logs[mid], entry) <= 0) lo = mid + 1
    else hi = mid
  }
  logs.splice(lo, 0, entry)
}

export const useRemoteLogStore = defineStore('remoteLog', () => {
  const sessions = ref<Map<string, GroupSession>>(new Map())

  function touchSessions() {
    sessions.value = new Map(sessions.value)
  }

  function makeSession(): GroupSession {
    return {
      refCount: 1,
      view: null,
      logs: [],
      sockets: new Map(),
      errors: new Map(),
      loadingHistory: false,
      ports: new Map(),
      oldestIds: new Map(),
      hasMoreHistoryByHost: new Map(),
    }
  }

  async function subscribe(logSourceId: string, groupKey: string) {
    const key = sessionKey(logSourceId, groupKey)
    const existing = sessions.value.get(key)
    if (existing) {
      existing.refCount++
      touchSessions()
      return
    }

    const session = makeSession()
    sessions.value.set(key, session)
    touchSessions()

    let view: RemoteViewResponse
    try {
      view = await api.getRemoteView(logSourceId)
    } catch (err) {
      session.errors.set('__view__', err instanceof Error ? err.message : '加载视图失败')
      touchSessions()
      return
    }
    session.view = view

    const group = view.groups.find(item => item.group_key === groupKey)
    if (!group) {
      session.errors.set('__group__', `分组 ${groupKey} 不存在`)
      touchSessions()
      return
    }

    await Promise.all(group.host_ids.map(hostId => connectHost(session, view, hostId)))
    touchSessions()
  }

  async function connectHost(session: GroupSession, view: RemoteViewResponse, hostId: string) {
    try {
      const tunnel = await api.openTunnel(hostId)
      if (!tunnel.local_port) throw new Error(`隧道未就绪：${tunnel.state}`)
      session.ports.set(hostId, tunnel.local_port)

      const service = encodeURIComponent(view.log_source.name)
      const ws = new WebSocket(`ws://127.0.0.1:${tunnel.local_port}/ws/logs?service=${service}`)
      session.sockets.set(hostId, ws)
      ws.onmessage = event => {
        try {
          const raw = JSON.parse(event.data) as LogEntry
          ingestLog(session, hostId, raw)
        } catch {
          session.errors.set(hostId, '日志帧解析失败')
          touchSessions()
        }
      }
      ws.onerror = () => {
        session.errors.set(hostId, 'WebSocket 错误')
        touchSessions()
      }
      ws.onclose = () => {
        session.sockets.delete(hostId)
        touchSessions()
      }
    } catch (err) {
      session.errors.set(hostId, err instanceof Error ? err.message : '连接失败')
      touchSessions()
    }
  }

  function ingestLog(session: GroupSession, hostId: string, raw: LogEntry) {
    insertSorted(session.logs, { ...raw, host_id: hostId })
    const oldest = session.oldestIds.get(hostId)
    if (oldest == null || raw.id < oldest) {
      session.oldestIds.set(hostId, raw.id)
    }
    session.logs = [...session.logs]
    touchSessions()
  }

  async function loadHistory(logSourceId: string, groupKey: string, limit = 200) {
    const session = sessions.value.get(sessionKey(logSourceId, groupKey))
    if (!session?.view || session.loadingHistory) return
    const group = session.view.groups.find(item => item.group_key === groupKey)
    if (!group) return

    session.loadingHistory = true
    touchSessions()
    try {
      await Promise.all(group.host_ids.map(hostId => loadHostHistory(session, hostId, limit)))
    } finally {
      session.loadingHistory = false
      session.logs = [...session.logs].sort(compareLogs)
      touchSessions()
    }
  }

  async function loadHostHistory(session: GroupSession, hostId: string, limit: number) {
    if (session.hasMoreHistoryByHost.get(hostId) === false) return
    const port = session.ports.get(hostId)
    if (!port || !session.view) return

    const qs = new URLSearchParams()
    qs.set('service', session.view.log_source.name)
    qs.set('limit', String(limit))
    const oldest = session.oldestIds.get(hostId)
    if (oldest != null) qs.set('before', String(oldest))

    try {
      const res = await fetch(`http://127.0.0.1:${port}/api/logs?${qs}`)
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      const logs = await res.json() as LogEntry[]
      for (let i = logs.length - 1; i >= 0; i--) {
        ingestLog(session, hostId, logs[i])
      }
      session.hasMoreHistoryByHost.set(hostId, logs.length >= limit)
    } catch (err) {
      session.errors.set(hostId, err instanceof Error ? err.message : '加载历史失败')
    }
  }

  function unsubscribe(logSourceId: string, groupKey: string) {
    const key = sessionKey(logSourceId, groupKey)
    const session = sessions.value.get(key)
    if (!session) return
    session.refCount = Math.max(0, session.refCount - 1)
    if (session.refCount > 0) {
      touchSessions()
      return
    }
    for (const ws of session.sockets.values()) {
      try {
        ws.close()
      } catch {
        /* ignore close errors */
      }
    }
    sessions.value.delete(key)
    touchSessions()
  }

  function logsOf(logSourceId: string, groupKey: string): RemoteLogEntry[] {
    return sessions.value.get(sessionKey(logSourceId, groupKey))?.logs ?? []
  }

  function errorOf(logSourceId: string, groupKey: string, hostId: string): string | undefined {
    return sessions.value.get(sessionKey(logSourceId, groupKey))?.errors.get(hostId)
  }

  function viewOf(logSourceId: string, groupKey: string): RemoteViewResponse | null {
    return sessions.value.get(sessionKey(logSourceId, groupKey))?.view ?? null
  }

  function isLoadingHistory(logSourceId: string, groupKey: string): boolean {
    return sessions.value.get(sessionKey(logSourceId, groupKey))?.loadingHistory ?? false
  }

  return {
    sessions,
    subscribe,
    unsubscribe,
    loadHistory,
    logsOf,
    errorOf,
    viewOf,
    isLoadingHistory,
  }
})
