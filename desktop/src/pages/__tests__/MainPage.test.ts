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
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/components/Sidebar/SidebarView.vue', () => ({
  default: { template: '<aside data-test="sidebar-stub" />' },
}))

vi.mock('@/components/Workspace/WorkspaceShell.vue', () => ({
  default: { template: '<main data-test="workspace-stub" />' },
}))

vi.mock('@/components/BottomBar.vue', () => ({
  default: { template: '<footer data-test="bottom-bar-stub" />' },
}))

describe('MainPage', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('hides sidebar and bottom bar while runtime workspace is maximized', () => {
    vi.spyOn(useAgentStore(), 'startPolling').mockImplementation(() => undefined)
    const workspace = useWorkspaceStore()
    workspace.openDeployment('dep-1', 'api · demo')
    workspace.setRuntimeWorkspaceMaximized(true)

    const wrapper = mount(MainPage, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="sidebar-stub"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="workspace-stub"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="bottom-bar-stub"]').exists()).toBe(false)
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
})
