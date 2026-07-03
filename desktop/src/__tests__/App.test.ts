/**
 * App 根组件测试首次引导自动跳转。
 *
 * 职责：
 *   - 验证主窗口未完成 onboarding 时自动进入引导页
 *   - 验证 popover 窗口不被主窗口引导逻辑打断
 *
 * 边界：
 *   - 不渲染真实路由页面
 *   - 不调用真实 agent
 */
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App.vue'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useSettingsStore } from '@/stores/settings'
import { installTestI18n } from '@/test-utils/i18n'

const routeState = vi.hoisted(() => ({ path: '/' }))
const replace = vi.hoisted(() => vi.fn())
const windowApiMock = vi.hoisted(() => ({
  close: vi.fn(),
  minimize: vi.fn(),
  startDragging: vi.fn(),
  toggleMaximize: vi.fn(),
}))
const menuApiMock = vi.hoisted(() => ({
  new: vi.fn(),
  popup: vi.fn(),
}))

function mockNavigatorPlatform(platform: string) {
  Object.defineProperty(window.navigator, 'platform', {
    configurable: true,
    value: platform,
  })
}

vi.mock('vue-router', () => ({
  RouterView: { template: '<main />' },
  useRoute: () => routeState,
  useRouter: () => ({ replace }),
}))

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => windowApiMock,
}))

vi.mock('@tauri-apps/api/menu', () => ({
  Menu: {
    new: menuApiMock.new,
  },
}))

vi.mock('@tauri-apps/api/dpi', () => ({
  LogicalPosition: class LogicalPosition {
    x: number
    y: number

    constructor(x: number, y: number) {
      this.x = x
      this.y = y
    }
  },
}))

describe('App', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    vi.clearAllMocks()
    routeState.path = '/'
    replace.mockReset()
    mockNavigatorPlatform('MacIntel')
    windowApiMock.close.mockResolvedValue(undefined)
    windowApiMock.minimize.mockResolvedValue(undefined)
    windowApiMock.startDragging.mockResolvedValue(undefined)
    windowApiMock.toggleMaximize.mockResolvedValue(undefined)
    menuApiMock.popup.mockResolvedValue(undefined)
    menuApiMock.new.mockResolvedValue({ popup: menuApiMock.popup })
  })

  it('redirects main window to onboarding when incomplete', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: false }
    })

    mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    expect(replace).toHaveBeenCalledWith('/onboarding')
  })

  it('does not redirect popover route', async () => {
    routeState.path = '/popover'
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: false }
    })

    mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    expect(replace).not.toHaveBeenCalled()
  })

  it('starts operation approval polling in the main window', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: true }
    })
    const approvals = useOperationApprovalStore()
    const loadPending = vi.spyOn(approvals, 'loadPending').mockResolvedValue(undefined)
    const startPolling = vi.spyOn(approvals, 'startPolling').mockImplementation(() => undefined)
    const stopPolling = vi.spyOn(approvals, 'stopPolling').mockImplementation(() => undefined)

    const wrapper = mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    expect(loadPending).toHaveBeenCalledWith(false)
    expect(startPolling).toHaveBeenCalledTimes(1)

    wrapper.unmount()
    expect(stopPolling).toHaveBeenCalledTimes(1)
  })

  it('does not start operation approval polling in the popover window', async () => {
    routeState.path = '/popover'
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: true }
    })
    const approvals = useOperationApprovalStore()
    const loadPending = vi.spyOn(approvals, 'loadPending').mockResolvedValue(undefined)
    const startPolling = vi.spyOn(approvals, 'startPolling').mockImplementation(() => undefined)
    const stopPolling = vi.spyOn(approvals, 'stopPolling').mockImplementation(() => undefined)

    const wrapper = mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    expect(loadPending).not.toHaveBeenCalled()
    expect(startPolling).not.toHaveBeenCalled()

    wrapper.unmount()
    expect(stopPolling).not.toHaveBeenCalled()
  })

  it('renders the Windows titlebar menu beside native window controls', async () => {
    mockNavigatorPlatform('Win32')
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: true }
    })

    const wrapper = mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    expect(wrapper.find('[data-test="app-titlebar"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="app-titlebar-menu"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="app-titlebar-menu-file"]').text()).toBe('文件')
    expect(wrapper.find('[data-test="app-titlebar-minimize"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="app-titlebar-maximize"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="app-titlebar-close"]').exists()).toBe(true)

    await wrapper.find('[data-test="app-titlebar-minimize"]').trigger('click')
    await wrapper.find('[data-test="app-titlebar-maximize"]').trigger('click')
    await wrapper.find('[data-test="app-titlebar-close"]').trigger('click')

    expect(windowApiMock.minimize).toHaveBeenCalledTimes(1)
    expect(windowApiMock.toggleMaximize).toHaveBeenCalledTimes(1)
    expect(windowApiMock.close).toHaveBeenCalledTimes(1)
  })

  it('keeps macOS and Linux out of the custom Windows titlebar', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: true }
    })

    mockNavigatorPlatform('MacIntel')
    const macWrapper = mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()
    expect(macWrapper.find('[data-test="app-titlebar"]').exists()).toBe(false)
    macWrapper.unmount()

    mockNavigatorPlatform('Linux x86_64')
    const linuxWrapper = mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()
    expect(linuxWrapper.find('[data-test="app-titlebar"]').exists()).toBe(false)
  })

  it('does not add the Windows titlebar to the tray popover route', async () => {
    routeState.path = '/popover'
    mockNavigatorPlatform('Win32')
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, artifact_keep_versions: 10, sample_seeded: true, onboarding_completed: true }
    })

    const wrapper = mount(App, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushPromises()

    expect(wrapper.find('[data-test="app-titlebar"]').exists()).toBe(false)
  })
})
