/**
 * agentStore 生命周期操作测试
 *
 * 职责：
 *   - 验证 start/stop/restart 成功后记录当前会话内的日志分割 marker
 *
 * 边界：
 *   - 不建立真实 HTTP 连接，API 层通过 mock 验证
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore } from '../agent'
import { useLogLifecycleStore } from '../logLifecycle'
import { useOperationApprovalStore } from '../operationApproval'
import { AgentAPIError, api, type Project, type Service } from '@/api/agent'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      startDeployment: vi.fn().mockResolvedValue(undefined),
      stopDeployment: vi.fn().mockResolvedValue(undefined),
      restartDeployment: vi.fn().mockResolvedValue(undefined),
      describeLanguageRuntimeSchema: vi.fn().mockResolvedValue({ language: 'go', version: 1, title: { key: 'runtime.go.title', default: 'Go' }, fields: [] }),
      listProjects: vi.fn().mockResolvedValue([]),
      listServices: vi.fn().mockResolvedValue([]),
      listOperationApprovals: vi.fn().mockResolvedValue([]),
    },
  }
})

function service(id: string, name: string, projectId: string): Service {
  return {
    id,
    project_id: projectId,
    name,
    status: '',
    required: false,
    order: 1,
  }
}

function project(id: string, name: string, services: Service[] = []): Project {
  return {
    id,
    name,
    root_path: `/tmp/${name}`,
    services,
  }
}

describe('agent deployment lifecycle markers', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('records lifecycle markers after successful deployment actions', async () => {
    const agent = useAgentStore()
    const lifecycle = useLogLifecycleStore()

    await agent.startDeployment('dep-1')
    await agent.stopDeployment('dep-1')
    await agent.restartDeployment('dep-1')

    expect(lifecycle.getMarkers('dep-1').map(m => m.kind)).toEqual(['start', 'stop', 'restart'])
  })

  it('passes deployment start/restart intent through to the API', async () => {
    const agent = useAgentStore()

    await agent.startDeployment('dep-1', 'debug_launch')
    await agent.restartDeployment('dep-1', 'start_normal')

    expect(api.startDeployment).toHaveBeenCalledWith('dep-1', 'debug_launch')
    expect(api.restartDeployment).toHaveBeenCalledWith('dep-1', 'start_normal')
  })

  it('caches language runtime schemas by language', async () => {
    const agent = useAgentStore()

    const first = await agent.describeLanguageRuntimeSchema('go')
    const second = await agent.describeLanguageRuntimeSchema('go')

    expect(first).toEqual(second)
    expect(api.describeLanguageRuntimeSchema).toHaveBeenCalledTimes(1)
  })

  it('does not record a marker when the API call fails', async () => {
    vi.mocked(api.startDeployment).mockRejectedValueOnce(new Error('boom'))
    const agent = useAgentStore()
    const lifecycle = useLogLifecycleStore()

    await expect(agent.startDeployment('dep-1')).rejects.toThrow('boom')

    expect(lifecycle.getMarkers('dep-1')).toEqual([])
  })

  it('captures approval_required responses for desktop prompts', async () => {
    const approval = {
      id: 'opa_1',
      status: 'pending',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_1',
        kind: 'runtime.start',
        target: { deployment_id: 'dep-prod' },
        target_summary: 'demo/prod/api',
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } as any
    vi.mocked(api.startDeployment).mockRejectedValueOnce(new AgentAPIError('approval required', 403, {
      code: 'approval_required',
      error: 'approval required',
      approval,
      plan: approval.plan,
    }))
    vi.mocked(api.listOperationApprovals).mockResolvedValueOnce([approval])

    const agent = useAgentStore()
    const approvals = useOperationApprovalStore()
    const lifecycle = useLogLifecycleStore()

    await agent.startDeployment('dep-prod')

    expect(approvals.pendingCount).toBe(1)
    expect(approvals.notice?.approval_id).toBe('opa_1')
    expect(lifecycle.getMarkers('dep-prod')).toEqual([])
  })

  it('轮询完整项目快照以同步 MCP 新建项目', async () => {
    vi.useFakeTimers()
    const agent = useAgentStore()
    const initial = project('proj-ui', 'ui', [service('svc-ui', 'ui', 'proj-ui')])
    const createdByMcp = project('proj-mcp', 'mcp-created', [service('svc-api', 'api', 'proj-mcp')])
    vi.mocked(api.listProjects)
      .mockResolvedValueOnce([initial])
      .mockResolvedValueOnce([initial, createdByMcp])
    vi.mocked(api.listServices).mockResolvedValue([initial.services[0], createdByMcp.services[0]])

    try {
      agent.startPolling()
      await Promise.resolve()
      await Promise.resolve()
      expect(agent.projects.map(p => p.id)).toEqual(['proj-ui'])

      await vi.advanceTimersByTimeAsync(2000)

      expect(agent.projects.map(p => p.id)).toEqual(['proj-ui', 'proj-mcp'])
      expect(agent.projectById('proj-mcp')?.services.map(s => s.name)).toEqual(['api'])
    } finally {
      agent.stopPolling()
      vi.useRealTimers()
    }
  })
})
