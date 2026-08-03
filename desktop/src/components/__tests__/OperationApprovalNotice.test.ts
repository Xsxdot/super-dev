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
import { useSettingsStore } from '@/stores/settings'
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

  it('lays out the notification as header, message, and action sections', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'browser_debug.open',
      target_summary: 'ai-hub / dev / admin',
      project_id: 'project_1',
    } as any
    const settings = useSettingsStore()
    settings.agentSettings = {
      log_retention_days: 7,
      artifact_keep_versions: 10,
      approval: {
        config_upsert: true,
        pipeline_upsert: true,
        pipeline_run: true,
        template_import: true,
        browser_debug_open: true,
        code_debug_open: true,
        code_debug_evaluate: true,
        grace_minutes: 15,
      },
    }

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const sections = wrapper.findAll('[data-test^="operation-approval-section-"]')
    expect(sections.map(section => section.attributes('data-test'))).toEqual([
      'operation-approval-section-header',
      'operation-approval-section-body',
      'operation-approval-section-actions',
    ])
    expect(wrapper.find('[data-test="operation-approval-section-header"]').text()).toContain('需要操作审批')
    expect(wrapper.find('[data-test="operation-approval-section-body"]').text()).toContain('ai-hub / dev / admin')
    expect(wrapper.find('[data-test="operation-approval-section-actions"]').text()).toContain('批准')
  })

  it('approves with project grace from the notification when selected', async () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'browser_debug.open',
      target_summary: 'ai-hub / dev / admin',
      project_id: 'project_1',
    } as any
    const settings = useSettingsStore()
    settings.agentSettings = {
      log_retention_days: 7,
      artifact_keep_versions: 10,
      approval: {
        config_upsert: true,
        pipeline_upsert: true,
        pipeline_run: true,
        template_import: true,
        browser_debug_open: true,
        code_debug_open: true,
        code_debug_evaluate: true,
        grace_minutes: 15,
      },
    }
    const approve = vi.spyOn(store, 'approve').mockResolvedValue({ grace_granted: true } as any)

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await wrapper.find('[data-test="operation-approval-grace"]').setValue(true)
    await wrapper.find('[data-test="operation-approval-approve"]').trigger('click')

    expect(approve).toHaveBeenCalledWith('opa_1', { grantGrace: true })
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

  it('switches to a grayed-out conflict notice and hides approve/reject when conflictNotice matches the active notice', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    store.conflictNotice = { id: 'opa_1', decidedBy: '远程控制面 A' }

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('[data-test="operation-approval-conflict"]').text()).toContain('已由 远程控制面 A 处理')
    expect(wrapper.find('[data-test="operation-approval-section-actions"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="operation-approval-approve"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="operation-approval-reject"]').exists()).toBe(false)
  })

  it('shows an unnamed conflict message when the winning control plane has no display name', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    store.conflictNotice = { id: 'opa_1', decidedBy: '' }

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const conflict = wrapper.find('[data-test="operation-approval-conflict"]')
    expect(conflict.text()).toContain('已处理')
    expect(conflict.text()).not.toContain('已由')
  })

  it('does not show the conflict view when conflictNotice belongs to a different approval', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    store.conflictNotice = { id: 'opa_other', decidedBy: '远程控制面 A' }

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('[data-test="operation-approval-conflict"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="operation-approval-section-actions"]').exists()).toBe(true)
  })

  it('shows retry copy when approval already succeeded but execution failed', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
      approved: true,
    }
    store.error = 'Load failed'

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.text()).toContain('继续执行失败')
    expect(wrapper.find('[data-test="operation-approval-approve"]').text()).toBe('重试')
  })

  // 纳管审批弹窗必须展示服务器侧推导的来源与配对码：自报名可以被伪造成真实桌面
  // 用的字符串，用户只能靠这两项确认弹出的是不是自己刚发起的那一条。
  it('shows the server-derived origin and pairing code for an adoption notice', () => {
    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_adopt',
      kind: 'agent.adopt',
      target_summary: 'adopt request from 203.0.113.9',
      request_origin: '203.0.113.9',
      pairing_code: 'K7QM4X',
    }

    const wrapper = mount(OperationApprovalNotice, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('[data-test="operation-approval-origin"]').text()).toContain('203.0.113.9')
    expect(wrapper.find('[data-test="operation-approval-pairing-code"]').text()).toContain('K7QM4X')
  })
})
