/**
 * WorkspaceShell.remote 测试远程 workspace tab 的渲染分支。
 *
 * 职责：
 *   - 验证 remote tab 渲染远程 LogPanel
 *
 * 边界：
 *   - 不测试 LogPanel 内部日志行为
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'

vi.mock('@/components/Panel/LogPanel.vue', () => ({
  default: {
    props: ['logSourceId', 'logSourceIds', 'groupKey', 'projectId'],
    template: '<div data-test="remote-log-panel">{{ logSourceId || (logSourceIds || []).join(",") }}:{{ groupKey }}:{{ projectId || "none" }}</div>',
  },
}))

vi.mock('@/components/Search/SearchPage.vue', () => ({
  default: {
    props: ['logSourceId', 'groupKey', 'tabId'],
    template: '<div data-test="remote-search-page">{{ logSourceId }}:{{ groupKey }}:{{ tabId }}</div>',
  },
}))

describe('WorkspaceShell remote tab', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('remote tab 渲染远程 LogPanel', () => {
    const workspace = useWorkspaceStore()
    workspace.openRemote('ls1', 'prod')

    const wrapper = mount(WorkspaceShell)

    expect(wrapper.find('[data-test="remote-log-panel"]').text()).toBe('ls1:prod:none')
  })


  it('remote tab passes bound project id from log source into LogPanel', () => {
    const remote = useRemoteStore()
    remote.logSources = [{
      id: 'ls-bound',
      name: 'remote api',
      type: 'journalctl',
      host_ids: [],
      tags: [],
      extra_args: [],
      project_id: 'project-a',
    }]
    const workspace = useWorkspaceStore()
    workspace.openRemote('ls-bound', 'all')

    const wrapper = mount(WorkspaceShell)

    expect(wrapper.find('[data-test="remote-log-panel"]').text()).toBe('ls-bound:all:project-a')
  })

  it('remote tab resolves project id through bound service when log source has only service_id', () => {
    useAgentStore().projects = [{
      id: 'project-a',
      name: 'Project A',
      root_path: '/tmp/project-a',
      selected_service_ids: [],
      services: [{
        id: 'service-api',
        project_id: 'project-a',
        name: 'api',
        status: 'running',
        command: 'pnpm dev',
        work_dir: '/tmp/project-a',
        required: false,
        order: 1,
      }],
    }]
    const remote = useRemoteStore()
    remote.logSources = [{
      id: 'ls-service',
      name: 'remote api',
      type: 'journalctl',
      host_ids: [],
      tags: [],
      extra_args: [],
      service_id: 'service-api',
    }]
    const workspace = useWorkspaceStore()
    workspace.openRemote('ls-service', 'all')

    const wrapper = mount(WorkspaceShell)

    expect(wrapper.find('[data-test="remote-log-panel"]').text()).toBe('ls-service:all:project-a')
  })

  it('remote-aggregate tab passes aggregate project id into LogPanel', () => {
    const workspace = useWorkspaceStore()
    workspace.openRemoteAggregate('project-a', 'service-api', 'api', ['ls-a', 'ls-b'], 'prod')

    const wrapper = mount(WorkspaceShell)

    expect(wrapper.find('[data-test="remote-log-panel"]').text()).toBe('ls-a,ls-b:prod:project-a')
  })

  it('remote-search tab 渲染远程 SearchPage', () => {
    const workspace = useWorkspaceStore()
    workspace.openRemoteSearch('ls1', 'prod')

    const wrapper = mount(WorkspaceShell)

    expect(wrapper.find('[data-test="remote-search-page"]').text()).toBe('ls1:prod:remote-search:ls1:prod')
  })
})
