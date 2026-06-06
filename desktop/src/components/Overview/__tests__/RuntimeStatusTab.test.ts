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

describe('RuntimeStatusTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale('en-US')
  })

  it('refreshes local runtime fallback on mount', () => {
    const store = useRuntimeStatusStore()
    const refresh = vi.spyOn(store, 'refresh').mockResolvedValue(undefined)

    mount(RuntimeStatusTab, { props: { project: projectWithDeployments(), active: true } })

    expect(refresh).toHaveBeenCalledWith('proj-1')
  })

  it('renders environment summary and instances from fallback plus node store', async () => {
    const store = useRuntimeStatusStore()
    vi.spyOn(store, 'refresh').mockResolvedValue(undefined)
    store.statusByProject['proj-1'] = {
      environments: [{
        env_name: 'dev',
        instances: [{
          service_id: 'svc-api',
          service_name: 'api',
          deployment_id: 'dep-local',
          node_id: 'local',
          node_name: 'local',
          is_local: true,
          metrics: { cpu_percent: 1, mem_bytes: 1024, uptime_sec: 1, restarts: 0, health: 'running', base: 'process' },
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

    expect(wrapper.text()).toContain('dev')
    expect(wrapper.text()).toContain('prod')
    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('1 abnormal')
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
})
