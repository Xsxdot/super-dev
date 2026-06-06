/**
 * runtimeStatusStore 管理项目概览页本地运行态快照 fallback。
 *
 * 职责：
 *   - 按 projectId 显式拉取 runtime-status 快照
 *   - 网络抖动时保留上一次成功数据
 *
 * 边界：
 *   - 不负责远端节点状态，远端实例由 nodeStore 投影
 *   - 不渲染实例卡片
 *   - 不持久化指标历史
 */
import { defineStore } from 'pinia'
import { reactive } from 'vue'
import { api, type RuntimeStatusResponse } from '@/api/agent'

export const useRuntimeStatusStore = defineStore('runtimeStatus', () => {
  const statusByProject = reactive<Record<string, RuntimeStatusResponse>>({})
  const loadingByProject = reactive<Record<string, boolean>>({})
  const errorByProject = reactive<Record<string, string | null>>({})
  const inFlight = new Set<string>()

  async function refresh(projectId: string) {
    if (inFlight.has(projectId)) return
    inFlight.add(projectId)
    loadingByProject[projectId] = true
    try {
      statusByProject[projectId] = await api.getRuntimeStatus(projectId)
      errorByProject[projectId] = null
    } catch (e) {
      errorByProject[projectId] = e instanceof Error ? e.message : 'Failed to update runtime status'
    } finally {
      loadingByProject[projectId] = false
      inFlight.delete(projectId)
    }
  }

  return {
    statusByProject,
    loadingByProject,
    errorByProject,
    refresh,
  }
})
