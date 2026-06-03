import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuntimeStatusTab from '../RuntimeStatusTab.vue'
import { useRuntimeStatusStore } from '@/stores/runtimeStatus'
import { setLocale } from '@/i18n'

describe('RuntimeStatusTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale('en-US')
  })

  it('starts polling on mount and stops on unmount', () => {
    const store = useRuntimeStatusStore()
    const start = vi.spyOn(store, 'start').mockImplementation(() => undefined)
    const stop = vi.spyOn(store, 'stop').mockImplementation(() => undefined)

    const wrapper = mount(RuntimeStatusTab, { props: { projectId: 'p1', active: true } })

    expect(start).toHaveBeenCalledWith('p1', 5000)
    wrapper.unmount()
    expect(stop).toHaveBeenCalledWith('p1')
  })

  it('renders environment summary and instances from store', () => {
    const store = useRuntimeStatusStore()
    vi.spyOn(store, 'start').mockImplementation(() => undefined)
    vi.spyOn(store, 'stop').mockImplementation(() => undefined)
    store.statusByProject.p1 = {
      environments: [{
        env_name: 'dev',
        instances: [
          {
            service_id: 'svc-api',
            service_name: 'api',
            deployment_id: 'dep-api',
            node_id: 'local',
            node_name: 'local',
            is_local: true,
            metrics: { cpu_percent: 1, mem_bytes: 1024, uptime_sec: 1, restarts: 0, health: 'running', base: 'process' },
          },
          {
            service_id: 'svc-web',
            service_name: 'web',
            deployment_id: 'dep-web',
            node_id: 'host-1',
            node_name: 'host-1',
            is_local: false,
            error: 'failed',
            metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: null, health: 'unknown', base: 'systemd' },
          },
        ],
      }],
    }

    const wrapper = mount(RuntimeStatusTab, { props: { projectId: 'p1', active: true } })

    expect(wrapper.text()).toContain('dev')
    expect(wrapper.text()).toContain('2 instances')
    expect(wrapper.text()).toContain('1 abnormal')
  })
})
