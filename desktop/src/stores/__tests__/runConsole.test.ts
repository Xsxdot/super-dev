/**
 * runConsole store 测试覆盖多 run 控制台状态。
 *
 * 职责：
 *   - 验证 runId 分桶隔离
 *   - 验证 WebSocket 信封事件分发
 *   - 验证日志按 id 去重、状态 patch、done 和 dispose
 *
 * 边界：
 *   - 不验证真实 WebSocket 后端路由
 */
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type Run } from '@/api/agent'
import { useRunConsoleStore } from '../runConsole'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      getProjectPipelineRun: vi.fn(),
      readProjectPipelineRunLogs: vi.fn(),
    },
    runLogsWsUrl: vi.fn((runId: string) => `ws://example/runs/${runId}/logs`),
  }
})

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  url: string
  onmessage: ((event: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.closed = true
  }
}

function run(partial: Partial<Run> = {}): Run {
  return {
    id: partial.id ?? 'run-1',
    project_id: 'p1',
    pipeline_id: 'deploy',
    env_name: 'dev',
    deployment_id: '',
    artifact_version: 'v1',
    status: partial.status ?? 'running',
    started_at: 1,
    step_runs: [{
      step_name: 'Deploy',
      type: 'local_command',
      phase: 'deploy',
      status: 'pending',
      tasks: [{ host_id: 'host-1', host_name: 'host-1', status: 'pending' }],
    }],
    ...partial,
  }
}

describe('runConsole store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('loads replay state into the requested run bucket', async () => {
    vi.mocked(api.getProjectPipelineRun).mockResolvedValue(run({ id: 'run-1', status: 'success' }))
    vi.mocked(api.readProjectPipelineRunLogs).mockResolvedValue({
      items: [{ id: 1, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'replay', at: 10 }],
    })
    const store = useRunConsoleStore()

    await store.loadReplay('p1', 'deploy', 'run-1')

    expect(store.stateFor('run-1').currentRun?.status).toBe('success')
    expect(store.visibleLogs('run-1').map(l => l.line)).toEqual(['replay'])
  })

  it('isolates selection and logs by run id', () => {
    const store = useRunConsoleStore()
    store.mergeRunLogs('run-1', [{ id: 1, run_id: 'run-1', step_name: 'Build', stream: 'stdout', line: 'one', at: 1 }])
    store.mergeRunLogs('run-2', [{ id: 2, run_id: 'run-2', step_name: 'Deploy', host_id: 'h1', stream: 'stdout', line: 'two', at: 2 }])
    store.select('run-2', 'Deploy', 'h1')

    expect(store.visibleLogs('run-1').map(l => l.line)).toEqual(['one'])
    expect(store.visibleLogs('run-2').map(l => l.line)).toEqual(['two'])
  })

  it('ingests log, status, and done websocket events', async () => {
    vi.mocked(api.getProjectPipelineRun).mockResolvedValue(run())
    vi.mocked(api.readProjectPipelineRunLogs).mockResolvedValue({ items: [] })
    const store = useRunConsoleStore()

    await store.loadLive('p1', 'deploy', 'run-1')
    FakeWebSocket.instances[0].onmessage?.({
      data: JSON.stringify({ kind: 'log', log: { id: 1, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'live', at: 10 } }),
    })
    FakeWebSocket.instances[0].onmessage?.({
      data: JSON.stringify({ kind: 'status', status: { step_name: 'Deploy', host_id: 'host-1', status: 'running' } }),
    })
    FakeWebSocket.instances[0].onmessage?.({
      data: JSON.stringify({ kind: 'done', run: run({ id: 'run-1', status: 'success', finished_at: 20 }) }),
    })

    const state = store.stateFor('run-1')
    expect(state.logs.map(l => l.line)).toEqual(['live'])
    expect(state.currentRun?.step_runs[0].tasks[0].status).toBe('running')
    expect(state.currentRun?.status).toBe('success')
    expect(state.ws).toBeNull()
  })

  it('deduplicates logs by id', () => {
    const store = useRunConsoleStore()
    store.mergeRunLogs('run-1', [{ id: 1, run_id: 'run-1', step_name: 'Deploy', stream: 'stdout', line: 'old', at: 10 }])
    store.mergeRunLogs('run-1', [{ id: 1, run_id: 'run-1', step_name: 'Deploy', stream: 'stdout', line: 'new', at: 11 }])

    expect(store.stateFor('run-1').logs).toHaveLength(1)
    expect(store.stateFor('run-1').logs[0].line).toBe('new')
  })

  it('disposes run state and closes websocket', async () => {
    vi.mocked(api.getProjectPipelineRun).mockResolvedValue(run())
    vi.mocked(api.readProjectPipelineRunLogs).mockResolvedValue({ items: [] })
    const store = useRunConsoleStore()

    await store.loadLive('p1', 'deploy', 'run-1')
    store.disposeRun('run-1')

    expect(FakeWebSocket.instances[0].closed).toBe(true)
    expect(store.hasRunState('run-1')).toBe(false)
  })
})
