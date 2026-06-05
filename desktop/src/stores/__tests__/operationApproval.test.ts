/**
 * 操作审批 Store 测试。
 *
 * 职责：
 *   - 验证 pending approvals 加载
 *   - 验证批准/拒绝后刷新
 *
 * 边界：
 *   - 不访问真实 agent API
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/agent'
import { useOperationApprovalStore } from '@/stores/operationApproval'

function pendingApproval(id = 'opa_1') {
  return {
    id,
    status: 'pending',
    requested_by: 'mcp',
    requester_label: 'Codex',
    plan: {
      id: 'op_1',
      kind: 'runtime.restart',
      target: { deployment_id: 'dep-prod' },
      target_summary: 'demo/prod/api',
      risk_level: 'high',
      requires_approval: true,
      denied: false,
      fingerprint: 'fp_1',
    },
  } as any
}

describe('operationApproval store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('loads pending approvals', async () => {
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([
      { id: 'opa_1', status: 'pending', plan: { id: 'op_1', kind: 'runtime.restart', target: {}, risk_level: 'high', requires_approval: true, denied: false, fingerprint: 'fp_1' } },
    ] as any)

    const store = useOperationApprovalStore()
    await store.loadPending()

    expect(store.pendingCount).toBe(1)
  })

  it('uses the first pending sync as baseline without showing a notice', async () => {
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([pendingApproval()])

    const store = useOperationApprovalStore()
    await store.syncPendingNotifications()

    expect(store.pendingCount).toBe(1)
    expect(store.notice).toBeNull()
  })

  it('shows a notice when a new MCP approval appears after baseline', async () => {
    vi.spyOn(api, 'listOperationApprovals')
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([pendingApproval()])

    const store = useOperationApprovalStore()
    await store.syncPendingNotifications()
    await store.syncPendingNotifications()

    expect(store.pendingCount).toBe(1)
    expect(store.notice).toEqual({
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'demo/prod/api',
    })
  })

  it('approves and refreshes pending approvals', async () => {
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ id: 'opa_1', status: 'approved' } as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', 'ok')

    expect(api.approveOperationApproval).toHaveBeenCalledWith('opa_1', { decided_by: 'user', note: 'ok' })
    expect(store.pendingCount).toBe(0)
  })

  it('resumes a desktop runtime operation after approval', async () => {
    const approval = {
      id: 'opa_1',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_1',
        kind: 'runtime.start',
        target: { deployment_id: 'dep-prod' },
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } as any
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue(approval)
    vi.spyOn(api, 'getOperationApproval').mockResolvedValue({ approval, approval_token: 'tok_1' })
    vi.spyOn(api, 'startDeployment').mockResolvedValue(undefined)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', 'ok')

    expect(api.getOperationApproval).toHaveBeenCalledWith('opa_1')
    expect(api.startDeployment).toHaveBeenCalledWith('dep-prod', 'tok_1')
    expect(store.error).toBe('')
  })

  it('retries execution without approving again after a resume failure', async () => {
    const approval = {
      id: 'opa_1',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_1',
        kind: 'runtime.start',
        target: { deployment_id: 'dep-prod' },
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } as any
    const approve = vi.spyOn(api, 'approveOperationApproval').mockResolvedValue(approval)
    vi.spyOn(api, 'getOperationApproval')
      .mockResolvedValueOnce({ approval, approval_token: 'tok_1' })
      .mockResolvedValueOnce({ approval, approval_token: 'tok_2' })
    vi.spyOn(api, 'startDeployment')
      .mockRejectedValueOnce(new Error('Load failed'))
      .mockResolvedValueOnce(undefined)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.start',
      target_summary: 'prod / api',
    }

    await store.approve('opa_1', 'ok')
    expect(store.notice?.approval_id).toBe('opa_1')
    expect(store.error).toBe('Load failed')

    await store.approve('opa_1', 'ok')

    expect(approve).toHaveBeenCalledTimes(1)
    expect(api.startDeployment).toHaveBeenNthCalledWith(1, 'dep-prod', 'tok_1')
    expect(api.startDeployment).toHaveBeenNthCalledWith(2, 'dep-prod', 'tok_2')
    expect(store.notice).toBeNull()
    expect(store.error).toBe('')
  })

  it('does not issue a token for MCP-requested approvals', async () => {
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({
      id: 'opa_1',
      status: 'approved',
      requested_by: 'mcp',
      requester_label: 'Codex',
      plan: {
        id: 'op_1',
        kind: 'runtime.restart',
        target: { deployment_id: 'dep-prod' },
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } as any)
    const getDetail = vi.spyOn(api, 'getOperationApproval').mockResolvedValue({} as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', '')

    expect(getDetail).not.toHaveBeenCalled()
  })

  it('clears the active notification after rejecting from the notification', async () => {
    vi.spyOn(api, 'rejectOperationApproval').mockResolvedValue({ id: 'opa_1', status: 'rejected' } as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    store.error = 'old error'
    await store.reject('opa_1', 'no')

    expect(api.rejectOperationApproval).toHaveBeenCalledWith('opa_1', { decided_by: 'user', note: 'no' })
    expect(store.notice).toBeNull()
    expect(store.error).toBe('')
  })

  it('records reject failures instead of throwing from notification actions', async () => {
    vi.spyOn(api, 'rejectOperationApproval').mockRejectedValue(new Error('reject failed'))
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    await store.reject('opa_1', '')

    expect(store.notice?.approval_id).toBe('opa_1')
    expect(store.error).toBe('reject failed')
  })
})
