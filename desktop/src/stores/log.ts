// logStore 按 serviceId 维护日志缓冲和 WebSocket 连接，多面板共享同一连接。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { WS_BASE, type LogEntry } from '@/api/agent'
import {
  closeActiveFold,
  ingest,
  toDisplayEntry,
  type DisplayLogEntry,
} from '@/lib/logEngine'
const MAX_LOGS = 5000
const TRIM_BATCH = 500

interface LogBoundary {
  timestamp: string
  id: number
}

interface ServiceLog {
  logs: DisplayLogEntry[]
  ws: WebSocket | null
  refCount: number
  bootstrapPromise: Promise<void> | null
  historyBoundary: LogBoundary | null
  seenSignatures: Set<string>
  // 最早已加载日志的 id，用于向上翻页
  oldestLoadedId: number | null
  // 是否还有更早的历史可加载
  hasMoreHistory: boolean
  loadingMoreHistory: boolean
}

export const useLogStore = defineStore('log', () => {
  const serviceLogs = ref<Record<string, ServiceLog>>({})
  const logSourceRevision = ref(0)

  function bumpRevision() {
    logSourceRevision.value++
  }

  function getOrCreate(serviceId: string): ServiceLog {
    if (!serviceLogs.value[serviceId]) {
      serviceLogs.value[serviceId] = {
        logs: [],
        ws: null,
        refCount: 0,
        bootstrapPromise: null,
        historyBoundary: null,
        seenSignatures: new Set(),
        oldestLoadedId: null,
        hasMoreHistory: true,
        loadingMoreHistory: false,
      }
    }
    return serviceLogs.value[serviceId]
  }

  function logSignature(log: LogEntry): string {
    return [
      log.service_id,
      log.run_id,
      log.timestamp,
      log.level,
      log.stream,
      log.message,
    ].join('\u001f')
  }

  function appendEntry(entry: ServiceLog, log: LogEntry): boolean {
    const sig = logSignature(log)
    if (entry.seenSignatures.has(sig)) return false
    entry.seenSignatures.add(sig)
    ingest(toDisplayEntry(log), entry.logs)
    if (entry.logs.length > MAX_LOGS) {
      const removed = entry.logs.splice(0, TRIM_BATCH)
      for (const r of removed) entry.seenSignatures.delete(logSignature(r))
    }
    if (entry.oldestLoadedId === null || log.id < entry.oldestLoadedId) {
      entry.oldestLoadedId = log.id
    }
    return true
  }

  function prependEntry(entry: ServiceLog, log: LogEntry): boolean {
    const sig = logSignature(log)
    if (entry.seenSignatures.has(sig)) return false
    entry.seenSignatures.add(sig)
    const display = toDisplayEntry(log)
    entry.logs.unshift(display)
    if (entry.oldestLoadedId === null || log.id < entry.oldestLoadedId) {
      entry.oldestLoadedId = log.id
    }
    return true
  }

  async function bootstrapRecent(serviceId: string, entry: ServiceLog) {
    if (entry.bootstrapPromise) return entry.bootstrapPromise
    entry.bootstrapPromise = (async () => {
      const { api } = await import('@/api/agent')
      let logs: LogEntry[] = []
      try {
        logs = await api.fetchLogs({ service: serviceId, limit: 200 })
      } catch (err) {
        console.warn('[SuperDev] load recent logs failed:', err)
        return
      }
      for (const log of logs) appendEntry(entry, log)
      const last = logs[logs.length - 1]
      entry.historyBoundary = last ? { timestamp: last.timestamp, id: last.id } : null
      // 如果返回条数等于 limit，说明可能还有更早的历史
      entry.hasMoreHistory = logs.length >= 200
      if (logs.length > 0) bumpRevision()
    })()
    return entry.bootstrapPromise
  }

  async function loadMoreHistory(serviceId: string) {
    const entry = serviceLogs.value[serviceId]
    if (!entry || entry.loadingMoreHistory || !entry.hasMoreHistory) return
    if (entry.oldestLoadedId === null) return
    entry.loadingMoreHistory = true
    try {
      const { api } = await import('@/api/agent')
      const logs = await api.fetchLogs({ service: serviceId, limit: 200, before: entry.oldestLoadedId })
      if (logs.length === 0) {
        entry.hasMoreHistory = false
        return
      }
      // 倒序插入到头部，保证时间顺序
      for (let i = logs.length - 1; i >= 0; i--) {
        prependEntry(entry, logs[i])
      }
      entry.hasMoreHistory = logs.length >= 200
      bumpRevision()
    } catch (err) {
      console.warn('[SuperDev] load more history failed:', err)
    } finally {
      entry.loadingMoreHistory = false
    }
  }

  async function subscribe(serviceId: string) {
    const entry = getOrCreate(serviceId)
    entry.refCount++
    if (entry.ws && entry.ws.readyState === WebSocket.OPEN) return
    await bootstrapRecent(serviceId, entry)
    if (entry.refCount <= 0) return
    if (entry.ws && entry.ws.readyState === WebSocket.OPEN) return
    const ws = new WebSocket(`${WS_BASE}/ws/logs?service=${serviceId}`)
    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data) as LogEntry
        if (appendEntry(entry, log)) bumpRevision()
      } catch {}
    }
    ws.onclose = () => {
      entry.ws = null
    }
    entry.ws = ws
  }

  function unsubscribe(serviceId: string) {
    const entry = serviceLogs.value[serviceId]
    if (!entry) return
    entry.refCount = Math.max(0, entry.refCount - 1)
    if (entry.refCount === 0 && entry.ws) {
      entry.ws.close()
      entry.ws = null
    }
  }

  function getLogs(serviceId: string): DisplayLogEntry[] {
    return serviceLogs.value[serviceId]?.logs ?? []
  }

  function getHistoryBoundary(serviceId: string): LogBoundary | null {
    return serviceLogs.value[serviceId]?.historyBoundary ?? null
  }

  function hasMoreHistory(serviceId: string): boolean {
    return serviceLogs.value[serviceId]?.hasMoreHistory ?? false
  }

  function isLoadingMoreHistory(serviceId: string): boolean {
    return serviceLogs.value[serviceId]?.loadingMoreHistory ?? false
  }

  function closeActiveFoldForService(serviceId: string) {
    const entry = serviceLogs.value[serviceId]
    if (!entry) return
    closeActiveFold(entry.logs)
    bumpRevision()
  }

  return {
    serviceLogs,
    logSourceRevision,
    subscribe,
    unsubscribe,
    getLogs,
    getHistoryBoundary,
    hasMoreHistory,
    isLoadingMoreHistory,
    loadMoreHistory,
    closeActiveFoldForService,
  }
})
