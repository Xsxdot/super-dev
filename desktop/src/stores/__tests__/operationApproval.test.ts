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

  it('approves and refreshes pending approvals', async () => {
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ id: 'opa_1', status: 'approved' } as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', 'ok')

    expect(api.approveOperationApproval).toHaveBeenCalledWith('opa_1', { decided_by: 'user', note: 'ok' })
    expect(store.pendingCount).toBe(0)
  })
})
