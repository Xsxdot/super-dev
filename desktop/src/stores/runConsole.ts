/**
 * runConsoleStore 管理 pipeline run 控制台状态。
 *
 * 职责：
 *   - 按 runId 隔离 run 详情、日志、选择状态和 WebSocket
 *   - 加载 replay 数据并接入 live RunEvent 信封
 *   - 对日志按 id 去重，对 step/host 状态做增量更新
 *
 * 边界：
 *   - 不执行或回滚流水线
 *   - 不管理 workspace tab 生命周期，只暴露 disposeRun 给 workspace 调用
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  api,
  runLogsWsUrl,
  type Run,
  type RunEvent,
  type RunLogLine,
  type RunStatusPatch,
  type StepRun,
} from '@/api/agent'

export interface RunConsoleState {
  currentRun: Run | null
  logs: RunLogLine[]
  selectedStep: string
  selectedHost: string
  loading: boolean
  error: string | null
  ws: WebSocket | null
  liveDone: boolean
  reconnectAttempted: boolean
}

function createRunState(): RunConsoleState {
  return {
    currentRun: null,
    logs: [],
    selectedStep: '',
    selectedHost: '',
    loading: false,
    error: null,
    ws: null,
    liveDone: false,
    reconnectAttempted: false,
  }
}

function compareRunLogs(a: RunLogLine, b: RunLogLine): number {
  return a.id - b.id
}

function aggregateStepStatus(step: StepRun): StepRun['status'] {
  if (step.tasks.length === 0) return step.status
  if (step.tasks.some(task => task.status === 'failed')) return 'failed'
  if (step.tasks.some(task => task.status === 'running')) return 'running'
  if (step.tasks.every(task => task.status === 'success')) return 'success'
  if (step.tasks.every(task => task.status === 'skipped')) return 'skipped'
  return step.status === 'running' ? 'running' : 'pending'
}

function mergeDoneRun(existing: Run | null, incoming: Run): Run {
  if (!existing) return incoming
  const currentSteps = new Map(existing.step_runs.map(step => [step.step_name, step]))
  const stepRuns = incoming.step_runs.map(step => {
    const currentStep = currentSteps.get(step.step_name)
    if (!currentStep) return step
    const currentTasks = new Map(
      currentStep.tasks.map(task => [task.host_id || '', task]),
    )
    const tasks = step.tasks.map(task => {
      const currentTask = currentTasks.get(task.host_id || '')
      if (!currentTask || task.status !== 'pending') return task
      // done 事件理论上携带最终 Run；这里仅兜底兼容旧/不完整快照，避免覆盖刚收到的增量状态。
      return { ...task, status: currentTask.status, exit_code: currentTask.exit_code ?? task.exit_code }
    })
    if (step.status !== 'pending') return { ...step, tasks }
    return { ...step, status: currentStep.status, tasks }
  })
  return { ...incoming, step_runs: stepRuns }
}

export const useRunConsoleStore = defineStore('runConsole', () => {
  const runs = ref(new Map<string, RunConsoleState>())

  function stateFor(runId: string): RunConsoleState {
    const existing = runs.value.get(runId)
    if (existing) return existing
    const created = createRunState()
    runs.value.set(runId, created)
    return created
  }

  function hasRunState(runId: string): boolean {
    return runs.value.has(runId)
  }

  function mergeRunLogs(runId: string, incoming: RunLogLine[]) {
    const state = stateFor(runId)
    const byID = new Map<number, RunLogLine>()
    for (const line of state.logs) byID.set(line.id, line)
    for (const line of incoming) byID.set(line.id, line)
    state.logs = [...byID.values()].sort(compareRunLogs)
  }

  function visibleLogs(runId: string): RunLogLine[] {
    const state = stateFor(runId)
    return state.logs.filter(line => {
      if (state.selectedStep && line.step_name !== state.selectedStep) return false
      if (state.selectedHost && line.host_id !== state.selectedHost) return false
      return true
    })
  }

  function select(runId: string, step: string, host = '') {
    const state = stateFor(runId)
    state.selectedStep = step
    state.selectedHost = host
  }

  async function loadReplay(projectId: string, pipelineId: string, runId: string) {
    const state = stateFor(runId)
    closeLive(runId)
    state.loading = true
    state.error = null
    try {
      state.currentRun = await api.getProjectPipelineRun(projectId, pipelineId, runId)
      state.logs = (await api.readProjectPipelineRunLogs(projectId, pipelineId, runId, { limit: 1000 })).items
    } catch (e) {
      state.error = e instanceof Error ? e.message : 'Failed to load run'
    } finally {
      state.loading = false
    }
  }

  async function loadLive(projectId: string, pipelineId: string, runId: string) {
    const state = stateFor(runId)
    closeLive(runId)
    state.loading = true
    state.error = null
    state.liveDone = false
    state.reconnectAttempted = false
    try {
      state.currentRun = await api.getProjectPipelineRun(projectId, pipelineId, runId)
      state.logs = []
      void connectLive(projectId, pipelineId, runId)
    } catch (e) {
      state.error = e instanceof Error ? e.message : 'Failed to load run'
    } finally {
      state.loading = false
    }
  }

  async function connectLive(projectId: string, pipelineId: string, runId: string) {
    const state = stateFor(runId)
    const url = await runLogsWsUrl(runId)
    // runLogsWsUrl 现在是 async（要经 Tauri IPC 读本机 token 才能拼 access_token）。
    // 等待期间该 run 可能已被 disposeRun 清理掉，此时放弃建连，
    // 否则会产生一条脱离 runs 状态表、没人会关闭的孤儿 WebSocket。
    if (runs.value.get(runId) !== state) return
    const ws = new WebSocket(url)
    state.ws = ws
    ws.onmessage = event => handleRunEvent(runId, JSON.parse(event.data) as RunEvent)
    ws.onerror = () => {
      ws.close()
    }
    ws.onclose = () => {
      if (state.liveDone || state.ws !== ws) return
      state.ws = null
      if (state.reconnectAttempted) return
      state.reconnectAttempted = true
      void loadReplay(projectId, pipelineId, runId).then(() => connectLive(projectId, pipelineId, runId))
    }
  }

  function handleRunEvent(runId: string, event: RunEvent) {
    const state = stateFor(runId)
    if (event.kind === 'log') {
      mergeRunLogs(runId, [event.log])
      return
    }
    if (event.kind === 'status') {
      applyStatusPatch(state, event.status)
      return
    }
    state.currentRun = mergeDoneRun(state.currentRun, event.run)
    state.liveDone = true
    closeLive(runId)
  }

  function applyStatusPatch(state: RunConsoleState, patch: RunStatusPatch) {
    if (!state.currentRun || !patch.step_name) return
    const step = state.currentRun.step_runs.find(item => item.step_name === patch.step_name)
    if (!step) return
    if (!patch.host_id) {
      step.status = patch.status
      return
    }
    const task = step.tasks.find(item => (item.host_id || '') === patch.host_id)
    if (!task) return
    task.status = patch.status
    if (patch.exit_code != null) task.exit_code = patch.exit_code
    step.status = aggregateStepStatus(step)
  }

  function closeLive(runId: string) {
    const state = stateFor(runId)
    const ws = state.ws
    state.ws = null
    ws?.close()
  }

  function disposeRun(runId: string) {
    closeLive(runId)
    runs.value.delete(runId)
  }

  return {
    runs,
    stateFor,
    hasRunState,
    visibleLogs,
    select,
    mergeRunLogs,
    loadReplay,
    loadLive,
    closeLive,
    disposeRun,
  }
})
