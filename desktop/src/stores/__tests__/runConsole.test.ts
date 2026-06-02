/**
 * runConsole store 测试覆盖单次 pipeline run 控制台状态。
 *
 * 职责：
 *   - 验证 run 详情和回放日志加载
 *   - 验证 step/host 选择过滤
 *   - 验证 live WS 失败不会清空回放日志
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
    runLogsWsUrl: vi.fn(() => 'ws://example/runs/run-1/logs'),
  }
})

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  url: string
  onmessage: ((event: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.closed = true
  }
}

function run(): Run {
  return {
    id: 'run-1',
    project_id: 'p1',
    pipeline_id: 'deploy',
    env_name: 'dev',
    deployment_id: '',
    artifact_version: 'v1',
    status: 'success',
    started_at: 1,
    step_runs: [{
      step_name: 'Deploy',
      type: 'local_command',
      phase: 'deploy',
      status: 'success',
      tasks: [{ host_id: 'host-1', host_name: 'host-1', status: 'success' }],
    }],
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

  it('loads run detail and replay logs', async () => {
    vi.mocked(api.getProjectPipelineRun).mockResolvedValue(run())
    vi.mocked(api.readProjectPipelineRunLogs).mockResolvedValue({
      items: [{ id: 1, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'deployed', at: 10 }],
    })
    const store = useRunConsoleStore()

    await store.loadReplay('p1', 'deploy', 'run-1')

    expect(store.currentRun?.id).toBe('run-1')
    expect(store.logs).toHaveLength(1)
    expect(store.logs[0].line).toBe('deployed')
  })

  it('filters visible logs by selected step and host', () => {
    const store = useRunConsoleStore()
    store.logs = [
      { id: 1, run_id: 'run-1', step_name: 'Build', host_id: 'host-1', stream: 'stdout', line: 'built', at: 1 },
      { id: 2, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'deployed', at: 2 },
      { id: 3, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-2', stream: 'stdout', line: 'other', at: 3 },
    ]
    store.select('Deploy', 'host-1')

    expect(store.visibleLogs.map(l => l.line)).toEqual(['deployed'])
  })

  it('keeps replay logs when live WebSocket fails', async () => {
    vi.mocked(api.getProjectPipelineRun).mockResolvedValue(run())
    vi.mocked(api.readProjectPipelineRunLogs).mockResolvedValue({
      items: [{ id: 1, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'replay', at: 10 }],
    })
    const store = useRunConsoleStore()

    await store.loadLive('p1', 'deploy', 'run-1')
    FakeWebSocket.instances[0].onerror?.()

    expect(store.logs.map(l => l.line)).toEqual(['replay'])
    expect(FakeWebSocket.instances[0].closed).toBe(true)
  })

  it('ingests live WebSocket log messages after replay data', async () => {
    vi.mocked(api.getProjectPipelineRun).mockResolvedValue(run())
    vi.mocked(api.readProjectPipelineRunLogs).mockResolvedValue({
      items: [{ id: 1, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'replay', at: 10 }],
    })
    const store = useRunConsoleStore()

    await store.loadLive('p1', 'deploy', 'run-1')
    FakeWebSocket.instances[0].onmessage?.({
      data: JSON.stringify({ id: 2, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'live', at: 11 }),
    })

    expect(store.logs.map(l => l.line)).toEqual(['replay', 'live'])
  })
})
