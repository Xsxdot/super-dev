// logStore 按 serviceId 维护日志缓冲和 WebSocket 连接，多面板共享同一连接。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { type LogEntry } from '@/api/agent'

const WS_BASE = 'ws://localhost:27017'
const MAX_LOGS = 8000  // 内存最多保留 8000 条

interface ServiceLog {
  logs: LogEntry[]
  ws: WebSocket | null
  refCount: number  // 订阅该 serviceId 的面板数量
}

export const useLogStore = defineStore('log', () => {
  const serviceLogs = ref<Record<string, ServiceLog>>({})
  // 按 serviceId 存历史查看的日志（与实时分开）
  const historyLogs = ref<Record<string, LogEntry[]>>({})

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
    // 建立 WebSocket 连接
    const ws = new WebSocket(`${WS_BASE}/ws/logs?service=${serviceId}`)
    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data) as LogEntry
        entry.logs.push(log)
        // 超出上限时从头部删除（环形缓冲效果）
        if (entry.logs.length > MAX_LOGS) {
          entry.logs.splice(0, entry.logs.length - MAX_LOGS)
        }
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

  function getLogs(serviceId: string): LogEntry[] {
    return serviceLogs.value[serviceId]?.logs ?? []
  }

  async function loadHistoryLogs(serviceId: string, runId: string) {
    const { api } = await import('@/api/agent')
    const logs = await api.fetchLogs({ service: serviceId, run: runId, limit: 2000 })
    historyLogs.value[serviceId] = logs
  }

  function clearHistoryLogs(serviceId: string) {
    delete historyLogs.value[serviceId]
  }

  function getHistoryLogs(serviceId: string): LogEntry[] {
    return historyLogs.value[serviceId] ?? []
  }

  // 获取某服务的所有历史 runId（从已缓存日志推断）
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
    return runs.reverse()  // 最新的在前
  }

  return {
    serviceLogs,
    historyLogs,
    subscribe,
    unsubscribe,
    getLogs,
    loadHistoryLogs,
    clearHistoryLogs,
    getHistoryLogs,
    getRunIds,
  }
})
