/**
 * OnboardingPage 测试零操作引导交互。
 *
 * 职责：
 *   - 验证智能体选择、安装按钮、复制提示词、完成动作
 *
 * 边界：
 *   - 不调用真实 Tauri command
 *   - 不启动 agent
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OnboardingPage from '../OnboardingPage.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import { useSettingsStore } from '@/stores/settings'
import { LOCALE_STORAGE_KEY } from '@/i18n'
import { installTestI18n } from '@/test-utils/i18n'

const push = vi.fn()
const windowApiMock = vi.hoisted(() => ({
  startDragging: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
}))

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => windowApiMock,
}))

describe('OnboardingPage', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    localStorage.clear()
    push.mockReset()
    windowApiMock.startDragging.mockReset()
    windowApiMock.startDragging.mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it('syncs onboarding language selection with settings', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'setLocale')
    const store = useOnboardingStore()
    vi.spyOn(store, 'detectInstalledAgents').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect((wrapper.find('[data-test="onboarding-locale-select"]').element as HTMLSelectElement).value).toBe('zh-CN')
    expect(wrapper.text()).toContain('选择你的编程智能体')

    await wrapper.find('[data-test="onboarding-locale-select"]').setValue('en-US')
    await nextTick()

    expect(settings.setLocale).toHaveBeenCalledWith('en-US')
    expect(settings.locale).toBe('en-US')
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('en-US')
    expect(wrapper.text()).toContain('Choose your coding agents')
  })

  it('starts native window dragging from onboarding chrome drag areas', async () => {
    vi.spyOn(useOnboardingStore(), 'detectInstalledAgents').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    await wrapper.find('[data-test="onboarding-header"]').trigger('mousedown', { buttons: 1 })

    expect(wrapper.find('[data-test="onboarding-header"]').attributes('data-tauri-drag-region')).toBe('deep')
    expect(windowApiMock.startDragging).toHaveBeenCalledTimes(1)
  })

  it('keeps onboarding form controls out of native dragging', async () => {
    vi.spyOn(useOnboardingStore(), 'detectInstalledAgents').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    await wrapper.find('[data-test="onboarding-locale-select"]').trigger('mousedown', { buttons: 1 })

    expect(windowApiMock.startDragging).not.toHaveBeenCalled()
  })

  it('shows installed agents as selectable and unavailable agents as disabled', async () => {
    const store = useOnboardingStore()
    vi.spyOn(store, 'detectInstalledAgents').mockResolvedValue(undefined)
    store.agentStatuses = {
      'claude-code': { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      codex: { agent: 'codex', installed: true, detection_path: '/usr/local/bin/codex' },
      cursor: { agent: 'cursor', installed: false, detection_path: null },
    }
    store.selectedAgents = ['claude-code']
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="agent-codex"]').trigger('click')
    await wrapper.find('[data-test="agent-cursor"]').trigger('click')
    await nextTick()

    expect(store.selectedAgents).toEqual(['claude-code', 'codex'])
    expect(wrapper.find('[data-test="agent-cursor"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="agent-cursor-status"]').text()).toBe('未检测到')
  })

  it('installs mcp for selected agents', async () => {
    const store = useOnboardingStore()
    vi.spyOn(store, 'detectInstalledAgents').mockResolvedValue(undefined)
    vi.spyOn(store, 'installSelectedMcp').mockResolvedValue(undefined)
    store.agentStatuses = {
      'claude-code': { agent: 'claude-code', installed: true, detection_path: '/usr/local/bin/claude' },
      codex: { agent: 'codex', installed: true, detection_path: '/usr/local/bin/codex' },
      cursor: { agent: 'cursor', installed: false, detection_path: null },
    }
    store.selectedAgents = ['claude-code', 'codex']
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="install-mcp"]').trigger('click')

    expect(store.installSelectedMcp).toHaveBeenCalled()
  })

  it('shows bundled skill installation result for successful mcp installs', async () => {
    const store = useOnboardingStore()
    vi.spyOn(store, 'detectInstalledAgents').mockResolvedValue(undefined)
    store.installOutcomes = [{
      agent: 'claude-code',
      installed: true,
      already_present: false,
      backup_path: null,
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{}}',
      skill: {
        installed: true,
        already_present: false,
        target_path: '/home/me/.claude/skills/superdev',
        backup_path: null,
        error: null,
      },
      session_hook: {
        installed: true,
        already_present: false,
        config_path: '/home/me/.claude/settings.json',
        backup_path: null,
        needs_trust: false,
        error: null,
      },
    }]

    const wrapper = mount(OnboardingPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect(wrapper.find('[data-test="skill-install-success"]').text()).toContain('使用指南 skill 已安装')
    expect(wrapper.find('[data-test="skill-install-success"]').text()).toContain('/home/me/.claude/skills/superdev')
  })

  it('shows degraded skill installation without turning mcp success into failure', async () => {
    const store = useOnboardingStore()
    vi.spyOn(store, 'detectInstalledAgents').mockResolvedValue(undefined)
    store.installOutcomes = [{
      agent: 'claude-code',
      installed: true,
      already_present: false,
      backup_path: null,
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{}}',
      skill: {
        installed: false,
        already_present: false,
        target_path: '/home/me/.claude/skills/superdev',
        backup_path: null,
        error: '找不到 SuperDev skill 资源目录，请检查桌面端打包配置',
      },
      session_hook: {
        installed: true,
        already_present: false,
        config_path: '/home/me/.claude/settings.json',
        backup_path: null,
        needs_trust: false,
        error: null,
      },
    }]

    const wrapper = mount(OnboardingPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect(wrapper.find('[data-test="install-success"]').text()).toContain('已装好')
    expect(wrapper.find('[data-test="skill-install-error"]').text()).toContain('使用指南 skill 未安装')
    expect(wrapper.find('[data-test="skill-install-error"]').text()).toContain('找不到 SuperDev skill')
  })

  it('copies prompt and marks completion', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'setOnboardingCompleted').mockResolvedValue(undefined)
    const store = useOnboardingStore()
    store.installOutcomes = [{
      agent: 'claude-code',
      installed: true,
      already_present: false,
      backup_path: null,
      config_path: '/home/me/.claude.json',
      manual_config: '{"mcpServers":{}}',
      skill: {
        installed: true,
        already_present: false,
        target_path: '/home/me/.claude/skills/superdev',
        backup_path: null,
        error: null,
      },
      session_hook: {
        installed: true,
        already_present: false,
        config_path: '/home/me/.claude/settings.json',
        backup_path: null,
        needs_trust: false,
        error: null,
      },
    }]
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="copy-prompt"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-test="copy-feedback"]').text()).toContain('已复制')

    await wrapper.find('[data-test="finish-onboarding"]').trigger('click')

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('superdev-sample'))
    expect(settings.setOnboardingCompleted).toHaveBeenCalledWith(true)
    expect(push).toHaveBeenCalledWith('/')
  })

  it('asks users to install mcp before confirming the prompt was sent', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'setOnboardingCompleted').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="finish-onboarding"]').trigger('click')
    await nextTick()

    expect(settings.setOnboardingCompleted).not.toHaveBeenCalled()
    expect(push).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="finish-feedback"]').text()).toContain('请先安装 MCP 连接')
  })

  it('skips onboarding from the bottom action', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'setOnboardingCompleted').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="skip-onboarding"]').trigger('click')

    expect(settings.setOnboardingCompleted).toHaveBeenCalledWith(true)
    expect(push).toHaveBeenCalledWith('/')
  })

  it('shows a visible error when skip cannot save completion', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'setOnboardingCompleted').mockRejectedValue(new Error('agent offline'))
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="skip-onboarding"]').trigger('click')
    await nextTick()

    expect(push).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="finish-feedback"]').text()).toContain('agent offline')
  })
})
