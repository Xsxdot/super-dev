// logStore 按 serviceId 维护日志缓冲和 WebSocket 连接，多面板共享同一连接。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { type LogEntry } from '@/api/agent'
import { ingest, toDisplayEntry, type DisplayLogEntry } from '@/lib/logEngine'

const WS_BASE = 'ws://127.0.0.1:27017'
const MAX_LOGS = 8000

interface ServiceLog {
  logs: DisplayLogEntry[]
  ws: WebSocket | null
  refCount: number
}

export const useLogStore = defineStore('log', () => {
  const serviceLogs = ref<Record<string, ServiceLog>>({})
  const historyLogs = ref<Record<string, DisplayLogEntry[]>>({})
  const logSourceRevision = ref(0)

  function bumpRevision() {
    logSourceRevision.value++
  }

  function getOrCreate(serviceId: string): ServiceLog {
    if (!serviceLogs.value[serviceId]) {
      serviceLogs.value[serviceId] = { logs: [], ws: null, refCount: 0 }
    }
    return serviceLogs.value[serviceId]
  }

  function subscribe(serviceId: string) {
    const entry = getOrCreate(serviceId)
    entry.refCount++
    if (entry.ws && entry.ws.readyState === WebSocket.OPEN) return
    const ws = new WebSocket(`${WS_BASE}/ws/logs?service=${serviceId}`)
    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data) as LogEntry
        ingest(toDisplayEntry(log), entry.logs)
        if (entry.logs.length > MAX_LOGS) {
          entry.logs.splice(0, entry.logs.length - MAX_LOGS)
        }
        bumpRevision()
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

  async function loadHistoryLogs(serviceId: string, runId: string) {
    const { api } = await import('@/api/agent')
    const logs = await api.fetchLogs({ service: serviceId, run: runId, limit: 2000 })
    const entries: DisplayLogEntry[] = []
    for (const log of logs) {
      ingest(toDisplayEntry(log), entries)
    }
    historyLogs.value[serviceId] = entries
    bumpRevision()
  }

  function clearHistoryLogs(serviceId: string) {
    delete historyLogs.value[serviceId]
  }

  function getHistoryLogs(serviceId: string): DisplayLogEntry[] {
    return historyLogs.value[serviceId] ?? []
  }

  function getRunIds(serviceId: string): string[] {
    const logs = serviceLogs.value[serviceId]?.logs ?? []
    const seen = new Set<string>()
    const runs: string[] = []
    for (const log of logs) {
      if (!seen.has(log.run_id)) {
        seen.add(log.run_id)
        runs.push(log.run_id)
      }
    }
    return runs.reverse()
  }

  return {
    serviceLogs,
    historyLogs,
    logSourceRevision,
    subscribe,
    unsubscribe,
    getLogs,
    loadHistoryLogs,
    clearHistoryLogs,
    getHistoryLogs,
    getRunIds,
  }
})
