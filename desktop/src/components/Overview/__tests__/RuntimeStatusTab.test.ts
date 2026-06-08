import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuntimeStatusTab from '../RuntimeStatusTab.vue'
import { useRuntimeStatusStore } from '@/stores/runtimeStatus'
import { useNodeStore } from '@/stores/node'
import { setLocale } from '@/i18n'
import type { Project } from '@/api/agent'

function projectWithDeployments(): Project {
  return {
    id: 'proj-1',
    name: 'Demo',
    root_path: '/tmp/demo',
    environments: [
      { id: 'env-dev', name: 'dev', is_dev: true, order: 1 },
      { id: 'env-prod', name: 'prod', is_dev: false, order: 2 },
    ],
    services: [{
      id: 'svc-api',
      project_id: 'proj-1',
      name: 'api',
      status: '',
      required: true,
      order: 1,
      deployments: [
        { id: 'dep-local', env_name: 'dev', location: 'local', status: '' },
        { id: 'dep-api', env_name: 'prod', location: 'remote', host_ids: ['h1'], status: '' },
      ],
    }],
  }
}

function remoteProject(): Project {
  return {
    ...projectWithDeployments(),
    services: [{
      ...projectWithDeployments().services[0],
      deployments: [{ id: 'dep-api', env_name: 'prod', location: 'remote', host_ids: ['h1'], status: '' }],
    }],
  }
}

function pivotProject(): Project {
  return {
    id: 'proj-pivot',
    name: 'Pivot Demo',
    root_path: '/tmp/pivot',
    environments: [
      { id: 'env-prod', name: 'prod', is_dev: false, order: 1 },
      { id: 'env-dev', name: 'dev', is_dev: true, order: 2 },
    ],
    services: [
      {
        id: 'svc-server',
        project_id: 'proj-pivot',
        name: 'server',
        status: '',
        required: true,
        order: 1,
        deployments: [
          { id: 'dep-server-prod', env_name: 'prod', location: 'local', status: '' },
          { id: 'dep-server-dev', env_name: 'dev', location: 'local', status: '' },
        ],
      },
      {
        id: 'svc-audio',
        project_id: 'proj-pivot',
        name: 'audio',
        status: '',
        required: true,
        order: 2,
        deployments: [
          { id: 'dep-audio-prod', env_name: 'prod', location: 'local', status: '' },
        ],
      },
    ],
  }
}

describe('RuntimeStatusTab', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    setLocale('en-US')
  })

  it('refreshes local runtime fallback on mount', () => {
    const store = useRuntimeStatusStore()
    const refresh = vi.spyOn(store, 'refresh').mockResolvedValue(undefined)

    mount(RuntimeStatusTab, { props: { project: projectWithDeployments(), active: true } })

    expect(refresh).toHaveBeenCalledWith('proj-1')
  })

  it('renders production service matrix while keeping local dev out of primary critical count', async () => {
    const store = useRuntimeStatusStore()
    vi.spyOn(store, 'refresh').mockResolvedValue(undefined)
    store.statusByProject['proj-1'] = {
      environments: [{
        env_name: 'dev',
        instances: [{
          service_id: 'svc-api',
          service_name: 'api',
          env_name: 'dev',
          deployment_id: 'dep-local',
          node_id: 'local',
          node_name: 'local',
          is_local: true,
          metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: null, health: 'stopped', base: 'process' },
        }],
      }],
    }
    const nodes = useNodeStore()
    nodes.applySnapshot([{
      host_id: 'h1',
      name: 'ali-01',
      reachable: true,
      agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
      deployments: [{
        service_id: 'svc-api',
        service_name: 'api',
        env_name: 'prod',
        deployment_id: 'dep-api',
        node_id: 'h1',
        node_name: 'ali-01',
        is_local: false,
        metrics: { cpu_percent: 1, mem_bytes: 1024, uptime_sec: 60, restarts: 0, health: 'unknown', base: 'systemd' },
      }],
      updated_at: '2026-06-06T10:00:00Z',
    }])

    const wrapper = mount(RuntimeStatusTab, { props: { project: projectWithDeployments(), active: true } })
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Service Matrix')
    expect(wrapper.find('[data-test="matrix-env-prod"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="matrix-env-dev"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="runtime-kpi-critical"]').text()).toContain('1')
    expect(wrapper.find('[data-test="runtime-kpi-local-dev"]').text()).toContain('0/1')
    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('Local Dev')
    expect(wrapper.text()).toContain('local')
  })

  it('renders remote instances from nodeStore without starting runtime polling', async () => {
    const runtime = useRuntimeStatusStore()
    const refreshSpy = vi.spyOn(runtime, 'refresh').mockResolvedValue(undefined)
    const nodes = useNodeStore()
    nodes.applySnapshot([{
      host_id: 'h1',
      name: 'ali-01',
      reachable: true,
      agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
      deployments: [{
        service_id: 'svc-api',
        service_name: 'api',
        env_name: 'prod',
        deployment_id: 'dep-api',
        node_id: 'h1',
        node_name: 'ali-01',
        is_local: false,
        metrics: { cpu_percent: 1, mem_bytes: 1024, uptime_sec: 60, restarts: 0, health: 'running', base: 'systemd' },
      }],
      updated_at: '2026-06-06T10:00:00Z',
    }])

    const wrapper = mount(RuntimeStatusTab, { props: { project: remoteProject(), active: true } })

    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('running')
    expect(refreshSpy).toHaveBeenCalledWith('proj-1')
  })

  it('renders service matrix and selected service node detail', async () => {
    const runtime = useRuntimeStatusStore()
    vi.spyOn(runtime, 'refresh').mockResolvedValue(undefined)
    runtime.statusByProject['proj-pivot'] = {
      environments: [
        {
          env_name: 'dev',
          instances: [{
            service_id: 'svc-server',
            service_name: 'server',
            env_name: 'dev',
            deployment_id: 'dep-server-dev',
            node_id: 'local',
            node_name: 'MacBook-Pro.local',
            is_local: true,
            metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: null, health: 'stopped', base: 'command' },
          }],
        },
        {
          env_name: 'prod',
          instances: [{
            service_id: 'svc-server',
            service_name: 'server',
            env_name: 'prod',
            deployment_id: 'dep-server-prod',
            node_id: 'local',
            node_name: 'local',
            is_local: true,
            metrics: { cpu_percent: 3.5, mem_bytes: 132 * 1024 * 1024, uptime_sec: 3600, restarts: 0, health: 'running', base: 'systemd' },
          }],
        },
      ],
    }

    const wrapper = mount(RuntimeStatusTab, { props: { project: pivotProject(), active: false } })
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Service Matrix')
    expect(wrapper.find('[data-test="matrix-env-prod"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="matrix-env-dev"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('server')
    expect(wrapper.text()).toContain('Running')

    await wrapper.find('[data-test="service-row-svc-server"]').trigger('click')

    expect(wrapper.text()).toContain('Local Dev')
    expect(wrapper.text()).toContain('MacBook-Pro.local')
  })

  it('selects a service row and opens logs from its node detail row', async () => {
    const runtime = useRuntimeStatusStore()
    vi.spyOn(runtime, 'refresh').mockResolvedValue(undefined)
    runtime.statusByProject['proj-pivot'] = {
      environments: [{
        env_name: 'prod',
        instances: [{
          service_id: 'svc-audio',
          service_name: 'audio',
          env_name: 'prod',
          deployment_id: 'dep-audio-prod',
          node_id: 'local',
          node_name: 'local',
          is_local: true,
          metrics: { cpu_percent: 1.2, mem_bytes: 64 * 1024 * 1024, uptime_sec: 60, restarts: 0, health: 'running', base: 'systemd' },
        }],
      }],
    }

    const wrapper = mount(RuntimeStatusTab, { props: { project: pivotProject(), active: false } })
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-test="service-row-svc-audio"]').trigger('click')
    await wrapper.find('[data-test="node-row-dep-audio-prod-local"]').trigger('click')

    expect(wrapper.emitted('open-logs')?.[0]).toEqual(['dep-audio-prod', 'local'])
  })
})
