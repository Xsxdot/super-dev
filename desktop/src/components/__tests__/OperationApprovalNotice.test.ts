/**
 * 操作审批通知组件测试。
 *
 * 职责：
 *   - 验证审批通知在触发受控操作时直接提供批准/拒绝动作
 *   - 验证通知动作委托给 operation approval store
 *
 * 边界：
 *   - 不访问真实 agent
 *   - 不验证审批恢复执行细节
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OperationApprovalNotice from '@/components/OperationApprovalNotice.vue'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { installTestI18n } from '@/test-utils/i18n'

describe('OperationApprovalNotice', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('approves directly from the notification', async () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    const approve = vi.spyOn(store, 'approve').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await wrapper.find('[data-test="operation-approval-approve"]').trigger('click')

    expect(approve).toHaveBeenCalledWith('opa_1', '')
  })

  it('rejects directly from the notification', async () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    const reject = vi.spyOn(store, 'reject').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await wrapper.find('[data-test="operation-approval-reject"]').trigger('click')

    expect(reject).toHaveBeenCalledWith('opa_1', '')
  })

  it('shows approval action errors in the notification', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    store.error = 'approval failed'

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.text()).toContain('approval failed')
  })
})
