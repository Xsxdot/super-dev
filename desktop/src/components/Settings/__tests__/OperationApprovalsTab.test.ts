/**
 * 操作审批 Tab 测试。
 *
 * 职责：
 *   - 验证 pending approval 展示
 *   - 验证 approve/reject 按钮调用 store
 *
 * 边界：
 *   - 不访问真实 agent API
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OperationApprovalsTab from '../OperationApprovalsTab.vue'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { installTestI18n } from '@/test-utils/i18n'

describe('OperationApprovalsTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('renders approval and approves it', async () => {
    const store = useOperationApprovalStore()
    store.approvals = [{
      id: 'opa_1',
      status: 'pending',
      requested_by: 'mcp',
      requester_label: 'Codex',
      plan: {
        id: 'op_1',
        kind: 'runtime.restart',
        target: { project_name: 'demo', deployment_id: 'api-prod' },
        target_summary: 'demo/prod/api',
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        reasons: ['environment is not marked as dev'],
        expected_effects: ['restart local deployment api-prod'],
        checks: [],
        fingerprint: 'sha256:abc',
      },
    } as any]
    vi.spyOn(store, 'loadPending').mockResolvedValue(undefined)
    const approve = vi.spyOn(store, 'approve').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalsTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await wrapper.find('[data-test="approval-approve-opa_1"]').trigger('click')

    expect(wrapper.text()).toContain('runtime.restart')
    expect(approve).toHaveBeenCalledWith('opa_1', '')
  })
})
