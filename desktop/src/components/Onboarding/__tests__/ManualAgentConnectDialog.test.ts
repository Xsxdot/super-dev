/**
 * ManualAgentConnectDialog 测试未知本机 Agent 的手动接入流程。
 *
 * 职责：
 *   - 验证本机配置材料、云端限制、复制反馈和显式验证门
 *
 * 边界：
 *   - 不调用真实 Tauri command，不写 Agent 配置
 */
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ManualAgentConnectDialog from '../ManualAgentConnectDialog.vue'
import { getGenericMcpConnectionMaterial } from '@/api/mcpInstall'
import { installTestI18n } from '@/test-utils/i18n'
import { emitOnboardingDiagnostic } from '@/lib/onboardingDiagnostics'

vi.mock('@/api/mcpInstall', () => ({
  getGenericMcpConnectionMaterial: vi.fn(),
}))

vi.mock('@/lib/onboardingDiagnostics', () => ({
  emitOnboardingDiagnostic: vi.fn(),
}))

const material = {
  transport: 'stdio' as const,
  command: '/Applications/SuperDev.app/Contents/MacOS/superdev-mcp',
  agent_url: 'http://127.0.0.1:57017',
  manual_config: '{"mcpServers":{"superdev":{"command":"/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"}}}',
}

const mountedWrappers: Array<ReturnType<typeof mount>> = []

function mountDialog() {
  const wrapper = mount(ManualAgentConnectDialog, {
    props: { open: true },
    attachTo: document.body,
    global: { plugins: [installTestI18n('zh-CN')] },
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

describe('ManualAgentConnectDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getGenericMcpConnectionMaterial).mockResolvedValue(material)
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  afterEach(() => {
    for (const wrapper of mountedWrappers.splice(0)) wrapper.unmount()
    document.body.innerHTML = ''
    window.history.replaceState({}, '', '/')
  })

  it('打开时先要求选择运行环境且不展示本机配置', () => {
    const wrapper = mountDialog()

    expect(wrapper.find('[role="dialog"]').attributes('aria-modal')).toBe('true')
    expect(wrapper.find('[data-test="manual-env-local"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="manual-env-cloud"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="manual-config"]').exists()).toBe(false)
    expect(getGenericMcpConnectionMaterial).not.toHaveBeenCalled()
  })

  it('选择本机后加载标准材料并可复制 JSON', async () => {
    const wrapper = mountDialog()

    await wrapper.find('[data-test="manual-env-local"]').trigger('click')
    await flushPromises()

    expect(getGenericMcpConnectionMaterial).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="manual-transport"]').text()).toContain('stdio')
    expect(wrapper.find('[data-test="manual-command"]').text()).toContain('superdev-mcp')
    expect(wrapper.find('[data-test="manual-agent-url"]').text()).toContain('127.0.0.1:57017')
    expect(wrapper.find('[data-test="manual-config"]').text()).toContain('mcpServers')

    await wrapper.find('[data-test="manual-copy-config"]').trigger('click')

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(material.manual_config)
    expect(wrapper.find('[data-test="manual-copy-feedback"]').text()).toContain('已复制')
    expect(emitOnboardingDiagnostic).toHaveBeenCalledWith('manual.material.copied', 'info')
  })

  it('keeps preview material inside the onboarding dialog boundary', async () => {
    window.history.replaceState({}, '', '/?onboardingPreview=1#/onboarding')
    const wrapper = mountDialog()

    await wrapper.find('[data-test="manual-env-local"]').trigger('click')
    await flushPromises()

    expect(getGenericMcpConnectionMaterial).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="manual-config"]').text()).toContain('/Applications/SuperDev.app')
  })

  it('选择云端时只展示限制且不请求本机材料', async () => {
    const wrapper = mountDialog()

    await wrapper.find('[data-test="manual-env-cloud"]').trigger('click')

    expect(getGenericMcpConnectionMaterial).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="manual-cloud-limit"]').text()).toContain('Remote MCP Gateway')
    expect(wrapper.find('[data-test="manual-config"]').exists()).toBe(false)
  })

  it('从本机切换到云端会清除材料和验证状态', async () => {
    const wrapper = mountDialog()
    await wrapper.find('[data-test="manual-env-local"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="manual-verified-checkbox"]').setValue(true)

    await wrapper.find('[data-test="manual-env-cloud"]').trigger('click')

    expect(wrapper.find('[data-test="manual-config"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="manual-verified-checkbox"]').exists()).toBe(false)
    expect(wrapper.emitted('verified')).toBeUndefined()
  })

  it('本机材料尚未返回时切到云端会丢弃过期响应', async () => {
    let resolveMaterial!: (value: typeof material) => void
    vi.mocked(getGenericMcpConnectionMaterial).mockReturnValue(new Promise<typeof material>((resolve) => {
      resolveMaterial = resolve
    }))
    const wrapper = mountDialog()

    await wrapper.find('[data-test="manual-env-local"]').trigger('click')
    await wrapper.find('[data-test="manual-env-cloud"]').trigger('click')
    resolveMaterial(material)
    await flushPromises()

    expect(wrapper.find('[data-test="manual-cloud-limit"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="manual-config"]').exists()).toBe(false)
    expect(emitOnboardingDiagnostic).not.toHaveBeenCalledWith(
      'manual.material.load.succeeded',
      'info',
      expect.anything(),
    )
  })

  it('只有显式确认在 Agent 内验证后才发出 verified', async () => {
    const wrapper = mountDialog()
    await wrapper.find('[data-test="manual-env-local"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="manual-confirm-verified"]').attributes('disabled')).toBeDefined()
    await wrapper.find('[data-test="manual-verified-checkbox"]').setValue(true)
    await wrapper.find('[data-test="manual-confirm-verified"]').trigger('click')

    expect(wrapper.emitted('verified')).toHaveLength(1)
    expect(emitOnboardingDiagnostic).toHaveBeenCalledWith('manual.connection.verified', 'info')
  })

  it('材料加载失败时展示上下文错误且不能验证', async () => {
    vi.mocked(getGenericMcpConnectionMaterial).mockRejectedValue(new Error('sidecar missing'))
    const wrapper = mountDialog()

    await wrapper.find('[data-test="manual-env-local"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="manual-load-error"]').text()).toContain('sidecar missing')
    expect(wrapper.find('[data-test="manual-confirm-verified"]').attributes('disabled')).toBeDefined()
    expect(emitOnboardingDiagnostic).toHaveBeenCalledWith('manual.material.failed', 'error', {
      errorCode: 'manual_material_load_failed',
      errorType: 'Error',
    })
  })

  it('在弹窗内循环 Tab 焦点并在关闭后回到触发控件', async () => {
    const opener = document.createElement('button')
    opener.textContent = 'open'
    document.body.appendChild(opener)
    opener.focus()
    const wrapper = mount(ManualAgentConnectDialog, {
      props: { open: false },
      attachTo: document.body,
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    mountedWrappers.push(wrapper)

    await wrapper.setProps({ open: true })
    await flushPromises()

    const dialog = wrapper.find('[role="dialog"]')
    const closeButton = wrapper.find('.icon-button')
    const localButton = wrapper.find('[data-test="manual-env-local"]')
    const cloudButton = wrapper.find('[data-test="manual-env-cloud"]')
    expect(document.activeElement).toBe(localButton.element)

    ;(cloudButton.element as HTMLElement).focus()
    await dialog.trigger('keydown', { key: 'Tab' })
    expect(document.activeElement).toBe(closeButton.element)

    ;(closeButton.element as HTMLElement).focus()
    await dialog.trigger('keydown', { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(cloudButton.element)

    await closeButton.trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(opener)
  })
})
