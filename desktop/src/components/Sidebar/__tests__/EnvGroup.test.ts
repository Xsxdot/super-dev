import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import EnvGroup from '@/components/Sidebar/EnvGroup.vue'
import { api } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { useNodeStore } from '@/stores/node'
import { installTestI18n } from '@/test-utils/i18n'
import type { Deployment, Health, NodeStatus, Service } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn(),
      getHostManagedDeploymentStatus: vi.fn(),
    },
  }
})

const mockedApi = api as unknown as Record<string, Mock>

const makeService = (
  id: string,
  name: string,
  envName: string,
  depExtra: Partial<Deployment> = {},
  serviceExtra: Partial<Service> = {},
): Service => ({
  id,
  project_id: 'proj-1',
  name,
  required: false,
  order: 0,
  status: '',
  deployments: [{ id: 'dep-' + id, env_name: envName, location: 'local', status: '', ...depExtra }],
  ...serviceExtra,
})

function makeNode(hostId: string, name: string, deploymentId: string, health: Health): NodeStatus {
  const running = health === 'running' || health === 'healthy' || health === 'restarting'
  return {
    host_id: hostId,
    name,
    reachable: true,
    agent: { installed: true, health: 'healthy', reachable: true },
    deployments: [{
      service_id: 'svc-1',
      service_name: 'api',
      env_name: 'prod',
      deployment_id: deploymentId,
      node_id: hostId,
      node_name: name,
      is_local: false,
      metrics: {
        cpu_percent: null,
        mem_bytes: null,
        uptime_sec: null,
        restarts: null,
        health,
        base: 'systemd',
      },
    }],
    managed: {
      deployment_count: 1,
      collector_count: 1,
      active_collector_count: running ? 1 : 0,
      collectors: [{
        deployment_id: deploymentId,
        desired: true,
        running,
        status: running ? 'running' : 'stopped',
      }],
    },
    updated_at: new Date(0).toISOString(),
  }
}

describe('EnvGroup', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.listHosts.mockResolvedValue([])
    mockedApi.getHostManagedDeploymentStatus.mockResolvedValue({
      host_id: 'h1',
      desired_deployment_count: 0,
      desired_collector_count: 0,
      active_collector_count: 0,
      tunnel_connected: true,
      remote: { deployment_count: 0, collector_count: 0, active_collector_count: 0, collectors: [] },
    })
  })
  it('is_dev=true 时初始展开，显示 service 行', () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev')],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-test="env-service-row"]').length).toBe(1)
  })

  it('is_dev=false 时初始折叠，不显示 service 行', () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'prod',
        isDev: false,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'prod')],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(false)
  })

  it('点击标题切换折叠状态', async () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'prod',
        isDev: false,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'prod')],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(false)
    await wrapper.find('[data-test="env-group-header"]').trigger('click')
    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(true)
  })

  it('点击 service 行 emit open-deployment（携带本 env 的 deploymentId）', async () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev')],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('click')

    const emitted = wrapper.emitted('open-deployment')
    expect(emitted).toBeTruthy()
    expect((emitted![0][0] as { deploymentId: string; title: string }).deploymentId).toBe('dep-svc-1')
    expect((emitted![0][0] as { deploymentId: string; title: string }).title).toBe('web · dev')
  })

  it('只读 deployment 不显示行内启停按钮', async () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev', { read_only: true })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
    expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(false)
  })

  it('停止状态显示启动按钮，点击后只启动 deployment 不打开日志', async () => {
    const agentStore = useAgentStore()
    const start = vi.spyOn(agentStore, 'startDeployment').mockResolvedValue(undefined)
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev', { status: '' })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
    await wrapper.find('[data-test="row-start"]').trigger('click')

    expect(start).toHaveBeenCalledWith('dep-svc-1')
    expect(wrapper.emitted('open-deployment')).toBeFalsy()
  })

  it('运行状态显示重启和停止按钮', async () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev', { status: 'running' })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
    expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)
  })

  it('远端单节点运行态为 running 时显示重启和停止按钮', async () => {
    const agentStore = useAgentStore()
    const stop = vi.spyOn(agentStore, 'stopDeploymentOnHost').mockResolvedValue(undefined)
    const nodeStore = useNodeStore()
    nodeStore.applySnapshot([makeNode('h1', 'prod-a', 'dep-svc-1', 'running')])
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'prod',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'api', 'prod', {
          location: 'remote',
          status: '',
          host_ids: ['h1'],
          control_mode: 'managed',
          runtime: { type: 'systemd', service_name: 'api' },
          start_command: 'systemctl start api',
          stop_command: 'systemctl stop api',
        })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await flushPromises()
    await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')

    expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)

    await wrapper.find('[data-test="row-stop"]').trigger('click')
    expect(stop).toHaveBeenCalledWith('dep-svc-1', 'h1')
    expect(wrapper.emitted('open-deployment')).toBeFalsy()
  })

  it('远端单节点只有 collector 运行证据时也显示重启和停止按钮', async () => {
    mockedApi.listHosts.mockResolvedValue([
      { id: 'h1', name: 'prod-a', private_ip: '10.0.0.1', tags: [] },
    ])
    mockedApi.getHostManagedDeploymentStatus.mockResolvedValue({
      host_id: 'h1',
      host_name: 'prod-a',
      desired_deployment_count: 1,
      desired_collector_count: 1,
      active_collector_count: 1,
      tunnel_connected: true,
      remote: {
        deployment_count: 1,
        collector_count: 1,
        active_collector_count: 1,
        collectors: [{
          deployment_id: 'dep-svc-1',
          desired: true,
          running: true,
          status: 'running',
        }],
      },
    })
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'prod',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'api', 'prod', {
          location: 'remote',
          status: '',
          host_ids: ['h1'],
          control_mode: 'managed',
          runtime: { type: 'systemd', service_name: 'api' },
          logs: { type: 'journalctl', target: 'api.service' },
          start_command: 'systemctl start api',
          stop_command: 'systemctl stop api',
        })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="service-meta"]').text()).toContain('Collector 1/1')
    expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)
  })

  it('远端多节点展开后在二级节点列表按节点运行态显示动作', async () => {
    const agentStore = useAgentStore()
    const stop = vi.spyOn(agentStore, 'stopDeploymentOnHost').mockResolvedValue(undefined)
    const nodeStore = useNodeStore()
    nodeStore.applySnapshot([
      makeNode('h1', 'prod-a', 'dep-svc-1', 'running'),
      makeNode('h2', 'prod-b', 'dep-svc-1', 'stopped'),
    ])
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'prod',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'api', 'prod', {
          location: 'remote',
          status: '',
          host_ids: ['h1', 'h2'],
          control_mode: 'managed',
          runtime: { type: 'systemd', service_name: 'api' },
          start_command: 'systemctl start api',
          stop_command: 'systemctl stop api',
        })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await flushPromises()
    await wrapper.find('[data-test="service-node-toggle"]').trigger('click')

    const nodeRows = wrapper.findAll('[data-test="env-node-leaf-row"]')
    expect(nodeRows).toHaveLength(2)
    expect(nodeRows[0].find('[data-test="node-row-restart"]').exists()).toBe(true)
    expect(nodeRows[0].find('[data-test="node-row-stop"]').exists()).toBe(true)
    expect(nodeRows[0].find('[data-test="node-row-start"]').exists()).toBe(false)
    expect(nodeRows[1].find('[data-test="node-row-start"]').exists()).toBe(true)
    expect(nodeRows[1].find('[data-test="node-row-restart"]').exists()).toBe(false)
    expect(nodeRows[1].find('[data-test="node-row-stop"]').exists()).toBe(false)

    await nodeRows[0].find('[data-test="node-row-stop"]').trigger('click')
    expect(stop).toHaveBeenCalledWith('dep-svc-1', 'h1')
    expect(wrapper.emitted('open-deployment')).toBeFalsy()
  })

  it('英文 locale 下渲染环境操作 tooltip', async () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev', { status: 'running' })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')

    expect(wrapper.find('[data-test="row-restart"]').attributes('title')).toBe('Restart')
    expect(wrapper.find('[data-test="row-stop"]').attributes('title')).toBe('Stop')
  })

  it('渲染 service 版本和副本数元信息', () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev', {}, { version: 'v1.2.3', replicas: 2 } as Partial<Service>)],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="service-meta"]').text()).toContain('v1.2.3')
    expect(wrapper.find('[data-test="service-meta"]').text()).toContain('2 replicas')
  })

  it('打开的 deployment 行保持高亮', () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev')],
        selectedServiceIds: new Set<string>(['dep-svc-1']),
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="env-service-row"]').classes()).toContain('selected')
  })

  it('环境标题显示服务数量，服务行使用 deployment card 结构', () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [
          makeService('svc-1', 'web', 'dev', { status: 'running' }),
          makeService('svc-2', 'worker', 'dev', { status: 'running' }),
        ],
        selectedServiceIds: new Set<string>(['dep-svc-1']),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="env-service-count"]').text()).toBe('2')
    expect(wrapper.find('[data-test="env-title"]').text()).toContain('dev')
    expect(wrapper.find('[data-test="env-title"]').text()).toContain('2')
    expect(wrapper.find('[data-test="env-actions"]').exists()).toBe(true)
    expect(wrapper.findAll('.deployment-card')).toHaveLength(2)
    expect(wrapper.find('.deployment-card').classes()).toContain('selected')
    expect(wrapper.find('[data-test="service-action-rail"]').exists()).toBe(true)
  })

  it('远端多节点 service 显示聚合状态并可展开节点叶子', async () => {
    mockedApi.listHosts.mockResolvedValue([
      { id: 'h1', name: 'ali-01', private_ip: '10.0.0.1', tags: [] },
      { id: 'h2', name: 'jp', private_ip: '10.0.0.2', tags: [] },
    ])
    mockedApi.getHostManagedDeploymentStatus.mockImplementation(async (hostId: string) => ({
      host_id: hostId,
      host_name: hostId,
      desired_deployment_count: 1,
      desired_collector_count: 1,
      active_collector_count: hostId === 'h1' ? 1 : 0,
      tunnel_connected: true,
      remote: {
        deployment_count: 1,
        collector_count: 1,
        active_collector_count: hostId === 'h1' ? 1 : 0,
        collectors: [{
          deployment_id: 'dep-svc-1',
          desired: true,
          running: hostId === 'h1',
          status: hostId === 'h1' ? 'running' : 'stopped',
        }],
      },
    }))

    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'prod',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'api', 'prod', {
          location: 'remote',
          status: 'running',
          host_ids: ['h1', 'h2'],
          logs: { type: 'file_tail', path: '/var/log/api.log' },
        })],
        selectedServiceIds: new Set<string>(),
      },
      global: { plugins: [installTestI18n()] },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="service-meta"]').text()).toContain('节点 1/2')
    expect(wrapper.find('[data-test="service-meta"]').text()).toContain('Collector 1/2')

    await wrapper.find('[data-test="service-node-toggle"]').trigger('click')

    expect(wrapper.findAll('[data-test="env-node-leaf-row"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="env-node-leaf-list"]').text()).toContain('ali-01')
    expect(wrapper.find('[data-test="env-node-leaf-list"]').text()).toContain('collector 未运行')
  })
})
