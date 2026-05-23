import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'
import { useWorkspaceStore } from '@/stores/workspace'
import { api, type Host, type LogSource, type Project, type Service } from '@/api/agent'

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
  message: vi.fn(),
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      remoteSearch: vi.fn().mockResolvedValue({
        query: 'trace-42',
        status: 'success',
        entries: [],
        total_by_host: {},
        hosts_failed: [],
        service_columns: [
          {
            service_id: 'svc-api',
            service_name: 'api',
            status: 'success',
            result_count: 1,
            node_count: 1,
            nodes: [{ host_id: 'host-a', host_name: 'Host A', status: 'success', count: 1 }],
            entries: [{
              id: 101,
              service_id: 'svc-api',
              run_id: 'run-a',
              timestamp: '2026-05-23T10:00:00.000Z',
              level: 'ERROR',
              message: 'api trace-42 hit',
              stream: 'stdout',
              host_id: 'host-a',
            }],
          },
          {
            service_id: 'svc-worker',
            service_name: 'worker',
            status: 'success',
            result_count: 1,
            node_count: 1,
            nodes: [{ host_id: 'host-a', host_name: 'Host A', status: 'success', count: 1 }],
            entries: [{
              id: 201,
              service_id: 'svc-worker',
              run_id: 'run-b',
              timestamp: '2026-05-23T10:00:01.000Z',
              level: 'INFO',
              message: 'worker trace-42 hit',
              stream: 'stdout',
              host_id: 'host-a',
            }],
          },
        ],
        failures: [],
        next_cursor: '',
        has_more: false,
      }),
    },
  }
})

function service(id: string, name: string): Service {
  return {
    id,
    project_id: 'project-a',
    name,
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'project-a',
    name: 'Project A',
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

function host(): Host {
  return {
    id: 'host-a',
    name: 'Host A',
    ssh_host: '10.0.0.1',
    ssh_port: 22,
    ssh_user: 'root',
    remote_agent_port: 57017,
    local_tunnel_port: 57018,
    tags: ['prod'],
  }
}

function hostB(): Host {
  return {
    id: 'host-b',
    name: 'Host B',
    ssh_host: '10.0.0.2',
    ssh_port: 22,
    ssh_user: 'root',
    remote_agent_port: 57017,
    local_tunnel_port: 57019,
    tags: ['prod'],
  }
}

function logSource(id: string, serviceId: string): LogSource {
  return {
    id,
    name: id,
    type: 'docker',
    host_ids: ['host-a'],
    tags: ['prod'],
    extra_args: [],
    project_id: 'project-a',
    service_id: serviceId,
  }
}

function logSourceOnHost(id: string, serviceId: string, hostId: string): LogSource {
  return {
    ...logSource(id, serviceId),
    host_ids: [hostId],
  }
}

describe('remote project search flow', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('clicks the remote project search entry, submits, and renders remote service columns', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    agent.projects = [project([service('svc-api', 'api'), service('svc-worker', 'worker')])]
    remote.hosts = [host()]
    remote.logSources = [logSource('ls-api', 'svc-api'), logSource('ls-worker', 'svc-worker')]

    const wrapper = mount({
      components: { SidebarView, WorkspaceShell },
      template: '<div><SidebarView /><WorkspaceShell /></div>',
    })

    await wrapper.find('[data-test="project-remote-search"]').trigger('click')
    await wrapper.find('[data-test="search-input"]').setValue('trace-42')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(api.remoteSearch as Mock).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-a',
      group: 'all',
      query: 'trace-42',
    }))
    expect(wrapper.text()).toContain('api trace-42 hit')
    expect(wrapper.text()).toContain('worker trace-42 hit')
    expect(wrapper.findAll('.column-header').map(header => header.text())).toEqual(
      expect.arrayContaining([expect.stringContaining('api'), expect.stringContaining('worker')]),
    )
  })

  it('blocks empty keyword submission with a visible prompt', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    agent.projects = [project([service('svc-api', 'api')])]
    remote.hosts = [host()]
    remote.logSources = [logSource('ls-api', 'svc-api')]

    const wrapper = mount({
      components: { SidebarView, WorkspaceShell },
      template: '<div><SidebarView /><WorkspaceShell /></div>',
    })

    await wrapper.find('[data-test="project-remote-search"]').trigger('click')
    await wrapper.find('[data-test="search-input"]').setValue('   ')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(api.remoteSearch as Mock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="remote-search-query-error"]').text()).toContain('请输入搜索内容')
  })

  it('renders service and node scope controls and sends selected scope when searching again', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    agent.projects = [project([service('svc-api', 'api'), service('svc-worker', 'worker')])]
    remote.hosts = [host(), hostB()]
    remote.logSources = [
      logSourceOnHost('ls-api-a', 'svc-api', 'host-a'),
      logSourceOnHost('ls-worker-b', 'svc-worker', 'host-b'),
    ]

    const wrapper = mount({
      components: { SidebarView, WorkspaceShell },
      template: '<div><SidebarView /><WorkspaceShell /></div>',
    })

    await wrapper.find('[data-test="project-remote-search"]').trigger('click')
    expect(wrapper.find('[data-test="remote-search-service-scope"]').text()).toContain('全部服务')
    expect(wrapper.find('[data-test="remote-search-node-scope"]').text()).toContain('全部节点')

    await wrapper.find('[data-test="remote-search-service-svc-api"]').setValue(true)
    await wrapper.find('[data-test="remote-search-node-host-a"]').setValue(true)
    await wrapper.find('[data-test="search-input"]').setValue('trace-42')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(api.remoteSearch as Mock).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-a',
      group: 'all',
      query: 'trace-42',
      service_id: ['svc-api'],
      host_id: ['host-a'],
    }))
  })

  it('highlights the searched keyword in remote project results', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    agent.projects = [project([service('svc-api', 'api')])]
    remote.hosts = [host()]
    remote.logSources = [logSource('ls-api', 'svc-api')]

    const wrapper = mount({
      components: { SidebarView, WorkspaceShell },
      template: '<div><SidebarView /><WorkspaceShell /></div>',
    })

    await wrapper.find('[data-test="project-remote-search"]').trigger('click')
    await wrapper.find('[data-test="search-input"]').setValue('trace-42')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    const marks = wrapper.findAll('[data-test="search-keyword-highlight"]')
    expect(marks.map(mark => mark.text())).toContain('trace-42')
  })

  it('shows no-searchable-object state and disables submission when the project has no searchable remote targets', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    agent.projects = [project([service('svc-api', 'api')])]
    remote.hosts = [host()]
    remote.logSources = []
    useWorkspaceStore().openRemoteProjectSearch('project-a', 'all')

    const wrapper = mount({
      components: { WorkspaceShell },
      template: '<WorkspaceShell />',
    })

    expect(wrapper.find('[data-test="remote-search-no-targets"]').text()).toContain('没有可搜索的远程日志对象')
    expect(wrapper.find('[data-test="remote-search-submit"]').attributes('disabled')).toBeDefined()
  })

  it('shows project remote pagination and requests the next page with the stored cursor', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    agent.projects = [project([service('svc-api', 'api')])]
    remote.hosts = [host()]
    remote.logSources = [logSource('ls-api', 'svc-api')]
    ;(api.remoteSearch as Mock)
      .mockResolvedValueOnce({
        query: 'trace-42',
        status: 'success',
        entries: [],
        total_by_host: {},
        hosts_failed: [],
        service_columns: [{
          service_id: 'svc-api',
          service_name: 'api',
          status: 'success',
          result_count: 1,
          node_count: 1,
          nodes: [{ host_id: 'host-a', host_name: 'Host A', status: 'success', count: 1 }],
          entries: [{
            id: 101,
            service_id: 'svc-api',
            run_id: 'run-a',
            timestamp: '2026-05-23T10:00:00.000Z',
            level: 'ERROR',
            message: 'first trace-42 hit',
            stream: 'stdout',
            host_id: 'host-a',
          }],
        }],
        failures: [],
        next_cursor: 'cursor-next',
        has_more: true,
      })
      .mockResolvedValueOnce({
        query: 'trace-42',
        status: 'success',
        entries: [],
        total_by_host: {},
        hosts_failed: [],
        service_columns: [{
          service_id: 'svc-api',
          service_name: 'api',
          status: 'success',
          result_count: 1,
          node_count: 1,
          nodes: [{ host_id: 'host-a', host_name: 'Host A', status: 'success', count: 1 }],
          entries: [{
            id: 102,
            service_id: 'svc-api',
            run_id: 'run-a',
            timestamp: '2026-05-23T10:00:01.000Z',
            level: 'ERROR',
            message: 'second trace-42 hit',
            stream: 'stdout',
            host_id: 'host-a',
          }],
        }],
        failures: [],
        next_cursor: '',
        has_more: false,
      })

    const wrapper = mount({
      components: { SidebarView, WorkspaceShell },
      template: '<div><SidebarView /><WorkspaceShell /></div>',
    })

    await wrapper.find('[data-test="project-remote-search"]').trigger('click')
    await wrapper.find('[data-test="search-input"]').setValue('trace-42')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(wrapper.find('[data-test="remote-search-has-more"]').text()).toContain('还有更多结果')

    await wrapper.find('[data-test="remote-search-load-more"]').trigger('click')
    await flushPromises()

    expect(api.remoteSearch as Mock).toHaveBeenLastCalledWith(expect.objectContaining({
      project_id: 'project-a',
      group: 'all',
      query: 'trace-42',
      cursor: 'cursor-next',
    }))
    expect(wrapper.text()).toContain('second trace-42 hit')
  })

})
