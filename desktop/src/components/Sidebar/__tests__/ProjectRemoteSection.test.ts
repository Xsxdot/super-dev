import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import ProjectRemoteSection from '@/components/Sidebar/ProjectRemoteSection.vue'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'
import type { Host, LogSource, Project, Service } from '@/api/agent'

function makeService(): Service {
  return {
    id: 'svc-api',
    project_id: 'project-a',
    name: 'api',
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function makeProject(service: Service): Project {
  return {
    id: 'project-a',
    name: 'Project A',
    root_path: '/tmp/project',
    services: [service],
    selected_service_ids: [],
  }
}

function makeHost(): Host {
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

function makeLogSource(id: string): LogSource {
  return {
    id,
    name: id,
    type: 'docker',
    host_ids: ['host-a'],
    tags: ['prod'],
    extra_args: [],
    project_id: 'project-a',
    service_id: 'svc-api',
  }
}

describe('ProjectRemoteSection', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('项目绑定远程聚合分组可作为 remote-aggregate payload 拖拽', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    const service = makeService()
    agent.projects = [makeProject(service)]
    remote.hosts = [makeHost()]
    remote.logSources = [makeLogSource('ls-a'), makeLogSource('ls-b')]

    const wrapper = mount(ProjectRemoteSection, {
      props: { projectId: 'project-a' },
    })

    const group = wrapper.find('[data-test="project-remote-group"]')
    expect(group.exists()).toBe(true)
    expect(group.attributes('draggable')).toBe('true')

    const dragData = new Map<string, string>()
    await group.trigger('dragstart', {
      dataTransfer: {
        setData: (type: string, value: string) => dragData.set(type, value),
      },
    })

    expect(JSON.parse(dragData.get('application/superdev-panel-source') ?? '{}')).toEqual({
      type: 'remote-aggregate',
      projectId: 'project-a',
      serviceId: 'svc-api',
      serviceName: 'api',
      logSourceIds: ['ls-a', 'ls-b'],
      groupKey: 'all',
    })
  })

  it('项目远程区块提供项目级远程搜索入口', async () => {
    const agent = useAgentStore()
    const remote = useRemoteStore()
    const service = makeService()
    agent.projects = [makeProject(service)]
    remote.hosts = [makeHost()]
    remote.logSources = [makeLogSource('ls-a')]

    const wrapper = mount(ProjectRemoteSection, {
      props: { projectId: 'project-a' },
    })

    await wrapper.find('[data-test="project-remote-search"]').trigger('click')

    expect(wrapper.emitted('search')).toEqual([[{ projectId: 'project-a', groupKey: 'all' }]])
  })
})
