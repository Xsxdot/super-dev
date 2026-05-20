/**
 * workspaceStore 测试右侧项目/搜索标签页状态。
 *
 * 职责：
 *   - 验证服务点击复用项目标签
 *   - 验证搜索按钮每次创建新的搜索标签
 *   - 验证隐藏服务和固定服务是搜索标签局部状态
 *
 * 边界：
 *   - 不渲染组件
 *   - 不建立真实 agent 连接
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useAgentStore } from '../agent'
import { usePanelStore } from '../panel'
import { useWorkspaceStore } from '../workspace'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string, projectId = 'proj-1'): Service {
  return {
    id,
    project_id: projectId,
    name,
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function project(services: Service[], id = 'proj-1', name = 'Project'): Project {
  return {
    id,
    name,
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

describe('workspaceStore', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('openService 创建项目标签并打开服务', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    workspace.openService('proj-1', 'svc-api')

    expect(workspace.tabs).toHaveLength(1)
    expect(workspace.activeTab?.type).toBe('project')
    expect(panel.allLeaves[0].serviceId).toBe('svc-api')
  })

  it('openService 复用已有项目标签', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore()

    workspace.openService('proj-1', 'svc-api')
    workspace.openService('proj-1', 'svc-worker')

    expect(workspace.tabs.filter(t => t.type === 'project')).toHaveLength(1)
  })

  it('openSearch 每次创建新的搜索标签', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    workspace.openSearch('proj-1')
    workspace.openSearch('proj-1')

    expect(workspace.tabs.filter(t => t.type === 'search')).toHaveLength(2)
    expect(workspace.activeTab?.type).toBe('search')
  })

  it('项目标签切换时恢复各自的 panel root', () => {
    const api = service('svc-api', 'api', 'proj-1')
    const admin = service('svc-admin', 'admin', 'proj-2')
    useAgentStore().projects = [
      project([api], 'proj-1', 'Project A'),
      project([admin], 'proj-2', 'Project B'),
    ]
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    workspace.openService('proj-1', 'svc-api')
    const firstTabId = workspace.activeTabId
    workspace.openService('proj-2', 'svc-admin')
    expect(panel.allLeaves[0].serviceId).toBe('svc-admin')

    workspace.activateTab(firstTabId!)

    expect(panel.allLeaves[0].serviceId).toBe('svc-api')
  })

  it('搜索标签的隐藏和固定服务互不影响', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    const first = workspace.openSearch('proj-1')
    workspace.hideService(first.id, 'svc-api')
    workspace.pinService(first.id, 'svc-api')
    const second = workspace.openSearch('proj-1')

    expect(workspace.searchTab(first.id)?.hiddenServiceIds).toEqual(['svc-api'])
    expect(workspace.searchTab(first.id)?.pinnedServiceIds).toEqual(['svc-api'])
    expect(workspace.searchTab(second.id)?.hiddenServiceIds).toEqual([])
    expect(workspace.searchTab(second.id)?.pinnedServiceIds).toEqual([])
  })
})
