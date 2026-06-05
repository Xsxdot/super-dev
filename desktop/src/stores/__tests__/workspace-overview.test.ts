/**
 * workspaceStore Project Overview 标签测试。
 *
 * 职责：
 *   - 验证 Project Overview 作为 workspace tab 打开和复用
 *   - 验证切换 overview 不破坏已有日志 tab 布局状态
 *
 * 边界：
 *   - 不渲染真实组件
 *   - 不访问 agent HTTP API
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useAgentStore } from '../agent'
import { useWorkspaceStore } from '../workspace'

describe('workspace overview tabs', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('opens one project overview tab per project and reuses it', () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]
    const workspace = useWorkspaceStore()

    const first = workspace.openProjectOverview('proj-1')
    const second = workspace.openProjectOverview('proj-1')

    expect(first.id).toBe(second.id)
    expect(workspace.tabs).toHaveLength(1)
    expect(workspace.activeTabId).toBe(first.id)
    expect(workspace.activeTab?.type).toBe('overview')
    expect(workspace.activeTab?.title).toBe('Project Overview')
  })

  it('does not overwrite active log panel layout when switching to overview', () => {
    const workspace = useWorkspaceStore()
    const project = workspace.ensureProjectTab('proj-1')
    workspace.activateTab(project.id)
    const overview = workspace.openProjectOverview('proj-1')

    expect(workspace.activeTabId).toBe(overview.id)
    expect(workspace.tabs.find(tab => tab.id === project.id)?.type).toBe('project')
  })
})
