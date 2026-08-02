/**
 * MainPage 运行态布局测试。
 *
 * 职责：
 *   - 验证运行态工作区最大化时隐藏主侧栏与底部栏
 *
 * 边界：
 *   - 子组件使用 stub，不验证侧栏、工作区和底部栏内部行为
 *   - 不启动真实 agent 轮询
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MainPage from '../MainPage.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { usePortMirrorStore } from '@/stores/portMirror'
import { installTestI18n } from '@/test-utils/i18n'

const windowApiMock = vi.hoisted(() => ({
  close: vi.fn(),
  minimize: vi.fn(),
  startDragging: vi.fn(),
  toggleMaximize: vi.fn(),
}))

function mockNavigatorPlatform(platform: string) {
  Object.defineProperty(window.navigator, 'platform', {
    configurable: true,
    value: platform,
  })
}

vi.mock('@/components/Sidebar/SidebarView.vue', () => ({
  default: { template: '<aside data-test="sidebar-stub" />' },
}))

vi.mock('@/components/Workspace/WorkspaceShell.vue', () => ({
  default: { template: '<main data-test="workspace-stub" />' },
}))

vi.mock('@/components/Workspace/WorkspaceTabs.vue', () => ({
  default: { template: '<nav data-test="workspace-tabs-stub" />' },
}))

vi.mock('@/components/BottomBar.vue', () => ({
  default: { template: '<footer data-test="bottom-bar-stub" />' },
}))

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => windowApiMock,
}))

describe('MainPage', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    vi.clearAllMocks()
    mockNavigatorPlatform('MacIntel')
    windowApiMock.startDragging.mockResolvedValue(undefined)
  })

  it('hides sidebar and bottom bar while runtime workspace is maximized', () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)
    const workspace = useWorkspaceStore()
    workspace.openDeployment('dep-1', 'api · demo')
    workspace.setRuntimeWorkspaceMaximized(true)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="app-topbar"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sidebar-stub"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="workspace-stub"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="bottom-bar-stub"]').exists()).toBe(false)
  })

  it('keeps app chrome visible before any workspace tab is opened', () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })
    const topbar = wrapper.find('[data-test="app-topbar"]')

    expect(topbar.exists()).toBe(true)
    expect(topbar.attributes('data-tauri-drag-region')).toBeDefined()
    expect(wrapper.find('[data-test="app-brand"]').text()).toContain('SuperDev')
    expect(wrapper.find('[data-test="workspace-tabs-stub"]').exists()).toBe(false)
  })

  it('starts native window dragging from app chrome drag areas', async () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="app-brand"]').trigger('mousedown', { buttons: 1 })

    expect(windowApiMock.startDragging).toHaveBeenCalledTimes(1)
  })

  it('renders the app chrome and workspace tabs in one compact top row', () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)
    const workspace = useWorkspaceStore()
    workspace.openDeployment('dep-1', 'api · demo')

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })
    const layout = wrapper.find('[data-test="main-layout"]').element
    const topbar = wrapper.find('[data-test="app-topbar"]').element
    const brand = wrapper.find('[data-test="app-brand"]').element
    const tabsRegion = wrapper.find('[data-test="app-tabs-region"]').element
    const contentRow = wrapper.find('[data-test="main-content-row"]').element
    const tabs = wrapper.find('[data-test="workspace-tabs-stub"]').element

    expect(topbar.parentElement).toBe(layout)
    expect(contentRow.parentElement).toBe(layout)
    expect(topbar.compareDocumentPosition(contentRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(topbar.contains(brand)).toBe(true)
    expect(topbar.contains(tabsRegion)).toBe(true)
    expect(tabsRegion.contains(tabs)).toBe(true)
    expect(topbar.contains(tabs)).toBe(true)
    expect(contentRow.contains(tabs)).toBe(false)
  })

  it('does not render the Windows menu inside the runtime app chrome', () => {
    mockNavigatorPlatform('Win32')
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="app-chrome-menu"]').exists()).toBe(false)
  })

  it('does not duplicate the native app menu on macOS shell builds', () => {
    mockNavigatorPlatform('MacIntel')
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="app-chrome-menu"]').exists()).toBe(false)
  })

  it('renders bottom bar below the sidebar/workspace row so it spans the full app width', () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })
    const layout = wrapper.find('[data-test="main-layout"]').element
    const contentRow = wrapper.find('[data-test="main-content-row"]').element
    const bottomBar = wrapper.find('[data-test="bottom-bar-stub"]').element

    expect(contentRow.parentElement).toBe(layout)
    expect(bottomBar.parentElement).toBe(layout)
    expect(contentRow.contains(bottomBar)).toBe(false)
  })

  it('starts the port mirror store subscription on setup, alongside nodeStore (fire-and-forget, app-lifetime)', () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)
    // 在 mount 之前先取得 store 实例再 spy——setActivePinia 已在 beforeEach 建好唯一活跃
    // 实例，MainPage.vue 内部 usePortMirrorStore() 拿到的是同一个单例，spy 才能命中。
    const startSpy = vi.spyOn(usePortMirrorStore(), 'start').mockResolvedValue(undefined)

    mount(MainPage, { global: { plugins: [installTestI18n()] } })

    // EnvGroup/BottomBar（Task 10）只读 portMirrorStore.mirrors，不启动订阅；没有页面级
    // 调用 start() 的话，mirrors 永远是空数组，端口镜像 UI 在真实 app 里就是空的。
    // 这条测试钉住页面级的启动调用，防止未来重构悄悄丢掉这行。
    expect(startSpy).toHaveBeenCalledTimes(1)
  })
})
