/**
 * WorkspaceTabs 组件测试工作区顶部标签标题。
 *
 * 职责：
 *   - 验证系统级 workspace tab 标题按当前语言渲染
 *   - 验证用户命名的项目 tab 标题不被翻译
 *
 * 边界：
 *   - 不渲染 tab 对应页面内容
 *   - 不验证 workspaceStore 的标签创建与复用逻辑
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import WorkspaceTabs from '../WorkspaceTabs.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

describe('WorkspaceTabs', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('renders system tab titles in the current locale', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]
    const workspace = useWorkspaceStore()
    workspace.ensureProjectTab('proj-1')
    workspace.openNodesTab()
    workspace.openProjectOverview('proj-1')
    const search = workspace.openSearch('proj-1')
    search.query = 'trace-8f21'
    search.title = 'Search: trace-8f21'

    const wrapper = mount(WorkspaceTabs, { global: { plugins: [installTestI18n('zh-CN')] } })

    const titles = wrapper.findAll('.tab-title').map(title => title.text())
    expect(titles).toContain('Demo')
    expect(titles).toContain('节点中心')
    expect(titles).toContain('项目概览')
    expect(titles).toContain('搜索：trace-8f21')
    expect(titles).not.toContain('Node Center')
    expect(titles).not.toContain('Project Overview')
    expect(titles).not.toContain('Search: trace-8f21')
  })
})
