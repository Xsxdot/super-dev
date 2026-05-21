// logStore 按 serviceId 维护日志缓冲和 WebSocket 连接，多面板共享同一连接。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { type LogEntry } from '@/api/agent'
import {
  closeActiveFold,
  ingest,
  toDisplayEntry,
  type DisplayLogEntry,
} from '@/lib/logEngine'

const WS_BASE = 'ws://127.0.0.1:27017'
const MAX_LOGS = 8000

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
      entry.logs.splice(0, entry.logs.length - MAX_LOGS)
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
      if (logs.length > 0) bumpRevision()
    })()
    return entry.bootstrapPromise
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
    closeActiveFoldForService,
  }
})
