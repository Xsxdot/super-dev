/**
 * runConsoleStore 管理单次 pipeline run 的控制台状态。
 *
 * 职责：
 *   - 加载 run 详情和回放日志
 *   - 管理步骤/主机选择
 *   - 在 live 模式下接入可用的 run WebSocket 日志
 *
 * 边界：
 *   - 不执行或回滚流水线
 *   - 不替代 pipeline run 持久化
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, runLogsWsUrl, type Run, type RunLogLine } from '@/api/agent'

export const useRunConsoleStore = defineStore('runConsole', () => {
  const currentRun = ref<Run | null>(null)
  const logs = ref<RunLogLine[]>([])
  const selectedStep = ref('')
  const selectedHost = ref('')
  const loading = ref(false)
  const error = ref<string | null>(null)
  let ws: WebSocket | null = null

  const visibleLogs = computed(() => logs.value.filter(line => {
    if (selectedStep.value && line.step_name !== selectedStep.value) return false
    if (selectedHost.value && line.host_id !== selectedHost.value) return false
    return true
  }))

  function select(step: string, host = '') {
    selectedStep.value = step
    selectedHost.value = host
  }

  async function loadReplay(projectId: string, pipelineId: string, runId: string) {
    loading.value = true
    error.value = null
    try {
      currentRun.value = await api.getProjectPipelineRun(projectId, pipelineId, runId)
      logs.value = (await api.readProjectPipelineRunLogs(projectId, pipelineId, runId, { limit: 1000 })).items
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load run'
    } finally {
      loading.value = false
    }
  }

  // 后端当前没有 /ws/runs/{runId}/logs 路由；live 模式先加载一次回放，WS 失败时保持已加载内容。
  // 这不是实时刷新实现，真正实时能力需要后端新增 run logs WS 路由后再开启。
  async function loadLive(projectId: string, pipelineId: string, runId: string) {
    await loadReplay(projectId, pipelineId, runId)
    closeLive()
    try {
      ws = new WebSocket(runLogsWsUrl(runId))
      ws.onmessage = event => {
        const line = JSON.parse(event.data) as RunLogLine
        logs.value = [...logs.value, line].sort((a, b) => a.id - b.id)
      }
      ws.onerror = () => {
        closeLive()
      }
    } catch {
      closeLive()
    }
  }

  function closeLive() {
    ws?.close()
    ws = null
  }

  function reset() {
    closeLive()
    currentRun.value = null
    logs.value = []
    selectedStep.value = ''
    selectedHost.value = ''
    loading.value = false
    error.value = null
  }

  return {
    currentRun,
    logs,
    selectedStep,
    selectedHost,
    visibleLogs,
    loading,
    error,
    select,
    loadReplay,
    loadLive,
    closeLive,
    reset,
  }
})
