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
import { AgentAPIError, api } from '@/api/agent'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      startDeployment: vi.fn().mockResolvedValue(undefined),
      stopDeployment: vi.fn().mockResolvedValue(undefined),
      restartDeployment: vi.fn().mockResolvedValue(undefined),
      listOperationApprovals: vi.fn().mockResolvedValue([]),
    },
  }
})

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
})
