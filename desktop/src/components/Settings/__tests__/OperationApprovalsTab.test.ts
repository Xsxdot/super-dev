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
import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
// @ts-expect-error Vitest runs this contract in Node, while app tsconfig intentionally omits Node types.
import { readFileSync } from 'node:fs'
import OperationApprovalsTab from '../OperationApprovalsTab.vue'
import { api } from '@/api/agent'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'
import { installTestI18n } from '@/test-utils/i18n'

declare const process: { cwd: () => string }

const operationApprovalsTabPath = `${process.cwd()}/src/components/Settings/OperationApprovalsTab.vue`

function operationApprovalsTabSource() {
  return readFileSync(operationApprovalsTabPath, 'utf8') as string
}

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

  it('uses shared settings card and button classes for approvals', async () => {
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
        reasons: ['Needs approval'],
        expected_effects: ['Service restarts'],
        checks: [],
        fingerprint: 'sha256:abcdef1234567890abcdef',
      },
    } as any]
    vi.spyOn(store, 'loadPending').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalsTab, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect(wrapper.find('.settings-pane-header').exists()).toBe(true)
    expect(wrapper.find('.settings-card').exists()).toBe(true)
    expect(wrapper.find('.settings-badge').exists()).toBe(true)
    expect(wrapper.find('[data-test="approval-approve-opa_1"]').classes()).toContain('settings-btn-primary')
    expect(wrapper.find('[data-test="approval-reject-opa_1"]').classes()).toContain('settings-btn-danger')
  })

  it('submits a complete approval policy from settings controls', async () => {
    const approvalStore = useOperationApprovalStore()
    vi.spyOn(approvalStore, 'loadPending').mockResolvedValue(undefined)
    const settingsStore = useSettingsStore()
    settingsStore.agentSettings = {
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
    vi.spyOn(settingsStore, 'loadAgentSettings').mockResolvedValue(undefined)
    const putSettings = vi.spyOn(api, 'putSettings').mockResolvedValue({
      log_retention_days: 7,
      artifact_keep_versions: 10,
      approval: {
        config_upsert: false,
        pipeline_upsert: true,
        pipeline_run: true,
        template_import: true,
        browser_debug_open: false,
        code_debug_open: true,
        code_debug_evaluate: true,
        grace_minutes: 30,
      },
    } as any)

    const wrapper = mount(OperationApprovalsTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await wrapper.find('[data-test="approval-switch-config-upsert"]').setValue(false)
    await wrapper.find('[data-test="approval-switch-browser-debug-open"]').setValue(false)
    await wrapper.find('[data-test="approval-grace-minutes"]').setValue(30)
    await wrapper.find('[data-test="approval-settings-save"]').trigger('click')

    expect(putSettings).toHaveBeenCalledWith({
      approval: {
        config_upsert: false,
        pipeline_upsert: true,
        pipeline_run: true,
        template_import: true,
        browser_debug_open: false,
        code_debug_open: true,
        code_debug_evaluate: true,
        grace_minutes: 30,
      },
    })
  })

  it('saves code debug approval policy switches', async () => {
    const approvalStore = useOperationApprovalStore()
    vi.spyOn(approvalStore, 'loadPending').mockResolvedValue(undefined)
    const settingsStore = useSettingsStore()
    settingsStore.agentSettings = {
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
    } as any
    vi.spyOn(settingsStore, 'loadAgentSettings').mockResolvedValue(undefined)
    const saveApprovalPolicy = vi.spyOn(settingsStore, 'saveApprovalPolicy').mockResolvedValue(undefined)

    const wrapper = mount(OperationApprovalsTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    await wrapper.get('[data-test="approval-switch-code-debug-open"]').setValue(false)
    await wrapper.get('[data-test="approval-switch-code-debug-evaluate"]').setValue(false)
    await wrapper.get('[data-test="approval-settings-save"]').trigger('click')

    expect(saveApprovalPolicy).toHaveBeenCalledWith(expect.objectContaining({
      code_debug_open: false,
      code_debug_evaluate: false,
    }))
  })

  it('shows a confirmation after saving approval policy', async () => {
    const approvalStore = useOperationApprovalStore()
    vi.spyOn(approvalStore, 'loadPending').mockResolvedValue(undefined)
    const settingsStore = useSettingsStore()
    settingsStore.agentSettings = {
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
    vi.spyOn(settingsStore, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(api, 'putSettings').mockResolvedValue({
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
    } as any)

    const wrapper = mount(OperationApprovalsTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await wrapper.find('[data-test="approval-settings-save"]').trigger('click')
    await flushPromises()

    const notice = wrapper.find('[data-test="approval-policy-save-notice"]')
    expect(notice.exists()).toBe(true)
    expect(notice.text()).toContain('审批策略已保存')
    expect(notice.classes()).toContain('settings-alert-success')
  })

  it('keeps approval policy checkbox labels adjacent to their controls', () => {
    const source = operationApprovalsTabSource()

    expect(source).toMatch(/\.policy-toggle\s*\{[^}]*justify-content:\s*flex-start;/s)
    expect(source).toMatch(/\.policy-toggle\s*\{[^}]*gap:\s*8px;/s)
    expect(source).not.toMatch(/\.policy-toggle,\s*\.policy-number\s*\{[^}]*justify-content:\s*space-between;/s)
  })
})
