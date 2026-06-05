/**
 * 操作审批浮层组件测试。
 *
 * 职责：
 *   - 验证审批浮层展示 pending approvals
 *   - 验证快速批准、拒绝和查看全部入口
 *
 * 边界：
 *   - 不访问真实 agent API
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OperationApprovalPopover from '@/components/OperationApprovalPopover.vue'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { installTestI18n } from '@/test-utils/i18n'

function pendingApproval() {
  return {
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
  } as any
}

describe('OperationApprovalPopover', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('renders pending approval and approves directly', async () => {
    const store = useOperationApprovalStore()
    store.approvals = [pendingApproval()]
    const approve = vi.spyOn(store, 'approve').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalPopover, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.text()).toContain('runtime.restart')
    expect(wrapper.text()).toContain('demo/prod/api')
    expect(wrapper.text()).toContain('environment is not marked as dev')

    await wrapper.find('[data-test="approval-popover-approve-opa_1"]').trigger('click')

    expect(approve).toHaveBeenCalledWith('opa_1', '')
  })

  it('rejects directly and emits view-all from the footer', async () => {
    const store = useOperationApprovalStore()
    store.approvals = [pendingApproval()]
    const reject = vi.spyOn(store, 'reject').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalPopover, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="approval-popover-reject-opa_1"]').trigger('click')
    await wrapper.find('[data-test="approval-popover-view-all"]').trigger('click')

    expect(reject).toHaveBeenCalledWith('opa_1', '')
    expect(wrapper.emitted('view-all')).toHaveLength(1)
  })

  it('shows empty and error states', () => {
    const store = useOperationApprovalStore()
    store.approvals = []
    store.error = 'load failed'

    const wrapper = mount(OperationApprovalPopover, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.text()).toContain('load failed')
    expect(wrapper.find('[data-test="approval-popover-empty"]').exists()).toBe(true)
  })
})
