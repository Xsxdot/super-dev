/**
 * NodeCenterView component tests.
 *
 * Responsibilities:
 *   - Verify remote hosts and node snapshots are merged into visible node cards
 *   - Verify the global view excludes local/self hosts
 *   - Verify clicking a service opens the existing deployment log workspace tab
 *
 * Boundaries:
 *   - Does not connect to real WebSocket streams
 *   - Does not call the backend when host state is injected by tests
 */
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import NodeCenterView from '../NodeCenterView.vue'
import nodeCenterViewSource from '../NodeCenterView.vue?raw'
import { installTestI18n } from '@/test-utils/i18n'
import { useAgentStore } from '@/stores/agent'
import { useNodeStore } from '@/stores/node'
import { useRemoteStore } from '@/stores/remote'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Host, NodeStatus, Project, RuntimeInstanceStatus } from '@/api/agent'

function host(partial: Partial<Host> = {}): Host {
  return {
    id: 'host-1',
    name: 'ali-01',
    private_ip: '10.0.0.8',
    tags: [],
    ...partial,
  }
}

function instance(partial: Partial<RuntimeInstanceStatus> = {}): RuntimeInstanceStatus {
  return {
    service_id: 'svc-api',
    service_name: 'api',
    deployment_id: 'dep-api',
    node_id: 'host-1',
    node_name: 'ali-01',
    is_local: false,
    metrics: {
      cpu_percent: 12.5,
      mem_bytes: 128 * 1024 * 1024,
      uptime_sec: 3661,
      restarts: 0,
      health: 'running',
      base: 'systemd',
    },
    ...partial,
  }
}

function node(partial: Partial<NodeStatus> = {}): NodeStatus {
  return {
    host_id: 'host-1',
    name: 'ali-01',
    reachable: true,
    agent: {
      installed: true,
      version: '0.1.0',
      health: 'healthy',
      reachable: true,
    },
    deployments: [instance()],
    updated_at: '2026-06-06T10:00:00Z',
    ...partial,
  }
}

function project(): Project {
  return {
    id: 'proj-1',
    name: 'Demo',
    root_path: '/tmp/demo',
    services: [{
      id: 'svc-api',
      project_id: 'proj-1',
      name: 'api',
      status: 'running',
      required: false,
      order: 1,
      deployments: [{ id: 'dep-api', env_name: 'prod', location: 'remote', status: 'running' }],
    }],
    environments: [{ id: 'env-prod', name: 'prod', is_dev: false, order: 1 }],
  }
}

function mountView() {
  return mount(NodeCenterView, {
    global: {
      plugins: [installTestI18n('en-US')],
    },
  })
}

describe('NodeCenterView', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('renders a configured host as a disconnected placeholder when no node snapshot exists', async () => {
    const remoteStore = useRemoteStore()
    remoteStore.hosts = [host()]

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="node-card-host-1"]').classes()).toContain('is-muted')
    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('Disconnected')
  })

  it('shows an empty state when there are no remote hosts or node snapshots', async () => {
    vi.spyOn(useRemoteStore(), 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="node-center-empty"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('No remote hosts configured')
  })

  it('excludes self hosts from the rendered grid', async () => {
    const remoteStore = useRemoteStore()
    remoteStore.hosts = [
      host({ id: 'self', name: 'local', is_self: true }),
      host({ id: 'host-1', name: 'ali-01' }),
    ]

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="node-card-self"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="node-card-host-1"]').exists()).toBe(true)
  })

  it('rerenders when nodeStore receives a live snapshot', async () => {
    const remoteStore = useRemoteStore()
    remoteStore.hosts = [host()]
    const nodeStore = useNodeStore()
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Disconnected')

    nodeStore.applySnapshot([node()])
    await nextTick()

    expect(wrapper.text()).toContain('api')
    expect(wrapper.text()).toContain('12.5%')
    expect(wrapper.text()).toContain('healthy')
  })

  it('opens the existing deployment log tab when a service row is clicked', async () => {
    useAgentStore().projects = [project()]
    const remoteStore = useRemoteStore()
    remoteStore.hosts = [host()]
    useNodeStore().applySnapshot([node()])
    const workspace = useWorkspaceStore()

    const wrapper = mountView()
    await flushPromises()
    await wrapper.find('[data-test="node-service-dep-api"]').trigger('click')

    expect(workspace.activeTab?.type).toBe('deployment')
    if (workspace.activeTab?.type !== 'deployment') throw new Error('expected deployment tab')
    expect(workspace.activeTab.deploymentId).toBe('dep-api')
    expect(workspace.activeTab.title).toBe('api · prod')
  })

  it('uses the full workspace width with a four-column adaptive cap', () => {
    expect(nodeCenterViewSource).toContain('width: 100%;')
    expect(nodeCenterViewSource).toContain('grid-template-columns: repeat(auto-fill')
    expect(nodeCenterViewSource).toContain('calc((100% - 36px) / 4)')
    expect(nodeCenterViewSource).not.toContain('1560px')
  })
})
