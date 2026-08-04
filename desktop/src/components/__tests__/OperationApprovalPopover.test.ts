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

function projectApproval() {
  const approval = pendingApproval()
  approval.plan.target.project_id = 'p1'
  return approval
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

  it('approves with project grace when selected', async () => {
    const store = useOperationApprovalStore()
    store.approvals = [projectApproval()]
    const approve = vi.spyOn(store, 'approve').mockResolvedValue({
      approval: { ...projectApproval(), status: 'approved' },
      grace_granted: true,
    } as any)

    const wrapper = mount(OperationApprovalPopover, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="approval-popover-grace-opa_1"]').setValue(true)
    await wrapper.find('[data-test="approval-popover-approve-opa_1"]').trigger('click')

    expect(approve).toHaveBeenCalledWith('opa_1', { grantGrace: true })
    expect(wrapper.text()).toContain('已对项目开启 15 分钟免审')
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

  // 浮层也能直接批准，纳管审批的防伪要素必须在这里同样可见。
  it('shows the server-derived origin and pairing code for an adoption approval', () => {
    const store = useOperationApprovalStore()
    store.approvals = [{
      id: 'opa_adopt',
      status: 'pending',
      requested_by: '203.0.113.9',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_adopt',
        kind: 'agent.adopt',
        target: { request_origin: '203.0.113.9', pairing_code: 'K7QM4X' },
        target_summary: 'adopt request from 203.0.113.9',
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'req-1',
      },
    } as any]

    const wrapper = mount(OperationApprovalPopover, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect(wrapper.find('[data-test="approval-popover-origin-opa_adopt"]').text()).toContain('203.0.113.9')
    expect(wrapper.find('[data-test="approval-popover-pairing-code-opa_adopt"]').text()).toContain('K7QM4X')
  })
})
