/**
 * runtimeStatusStore 管理项目概览页的运行态轮询。
 *
 * 职责：
 *   - 按 projectId 拉取 runtime-status 快照
 *   - 控制概览页挂载/卸载时的轮询生命周期
 *   - 网络抖动时保留上一次成功数据
 *
 * 边界：
 *   - 不渲染实例卡片
 *   - 不持久化指标历史
 */
import { defineStore } from 'pinia'
import { reactive } from 'vue'
import { api, type RuntimeStatusResponse } from '@/api/agent'

interface TimerEntry {
  timer: ReturnType<typeof setInterval>
  inFlight: boolean
}

export const useRuntimeStatusStore = defineStore('runtimeStatus', () => {
  const statusByProject = reactive<Record<string, RuntimeStatusResponse>>({})
  const loadingByProject = reactive<Record<string, boolean>>({})
  const errorByProject = reactive<Record<string, string | null>>({})
  const timers = new Map<string, TimerEntry>()

  async function refresh(projectId: string) {
    const entry = timers.get(projectId)
    if (entry?.inFlight) return
    if (entry) entry.inFlight = true
    loadingByProject[projectId] = true
    try {
      statusByProject[projectId] = await api.getRuntimeStatus(projectId)
      errorByProject[projectId] = null
    } catch (e) {
      errorByProject[projectId] = e instanceof Error ? e.message : 'Failed to update runtime status'
    } finally {
      loadingByProject[projectId] = false
      if (entry) entry.inFlight = false
    }
  }

  function start(projectId: string, intervalMs = 5000) {
    stop(projectId)
    timers.set(projectId, {
      inFlight: false,
      timer: setInterval(() => void refresh(projectId), intervalMs),
    })
    void refresh(projectId)
  }

  function stop(projectId: string) {
    const entry = timers.get(projectId)
    if (!entry) return
    clearInterval(entry.timer)
    timers.delete(projectId)
  }

  function stopAll() {
    for (const projectId of Array.from(timers.keys())) stop(projectId)
  }

  return {
    statusByProject,
    loadingByProject,
    errorByProject,
    refresh,
    start,
    stop,
    stopAll,
  }
})
