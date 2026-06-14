/**
 * ServiceDetailPane tests.
 *
 * Responsibilities:
 *   - Verify selected service node detail renders primary and local dev sections
 *   - Verify node row click emits open-logs with deployment and node IDs
 *
 * Boundaries:
 *   - Does not test service matrix row selection
 *   - Does not open workspace tabs directly
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ServiceDetailPane from '../ServiceDetailPane.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { ServiceMatrixRow } from '@/lib/runtimeServiceMatrix'

function row(): ServiceMatrixRow {
  return {
    serviceId: 'svc-server',
    serviceName: 'server',
    total: 2,
    abnormal: 0,
    cpuPercent: 3.5,
    memBytes: 132 * 1024 * 1024,
    nodeHealths: [],
    envs: [
      { envName: 'prod', total: 1, healthy: 1, abnormal: 0, debuggingCount: 0, health: 'running', label: 'Running 1/1', instances: [] },
    ],
    devEnvs: [
      { envName: 'dev', total: 1, healthy: 0, abnormal: 1, debuggingCount: 0, health: 'stopped', label: 'Stopped 0/1', instances: [] },
    ],
    instances: [
      {
        service_id: 'svc-server',
        service_name: 'server',
        env_name: 'prod',
        deployment_id: 'dep-prod',
        node_id: 'node-01',
        node_name: 'node-01',
        is_local: false,
        metrics: { cpu_percent: 3.5, mem_bytes: 132 * 1024 * 1024, uptime_sec: 162000, restarts: 0, health: 'running', base: 'systemd' },
      },
      {
        service_id: 'svc-server',
        service_name: 'server',
        env_name: 'dev',
        deployment_id: 'dep-dev',
        node_id: 'local',
        node_name: 'MacBook-Pro.local',
        is_local: true,
        metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: null, health: 'stopped', base: 'command' },
      },
    ],
  }
}

describe('ServiceDetailPane', () => {
  it('renders selected service summary, production nodes, and local dev separately', () => {
    const wrapper = mount(ServiceDetailPane, {
      props: { row: row(), environments: ['prod'], devEnvironments: ['dev'] },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('server')
    expect(wrapper.text()).toContain('Production / Remote')
    expect(wrapper.text()).toContain('Running 1/1')
    expect(wrapper.text()).toContain('node-01')
    expect(wrapper.text()).toContain('3.5%')
    expect(wrapper.text()).toContain('132 MiB')
    expect(wrapper.text()).toContain('45h 0m')
    expect(wrapper.text()).toContain('Local Dev')
    expect(wrapper.text()).toContain('MacBook-Pro.local')
    expect(wrapper.text()).toContain('Stopped 0/1')
  })

  it('emits open-logs when a node row is clicked', async () => {
    const wrapper = mount(ServiceDetailPane, {
      props: { row: row(), environments: ['prod'], devEnvironments: ['dev'] },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="node-row-dep-prod-node-01"]').trigger('click')

    expect(wrapper.emitted('open-logs')?.[0]).toEqual(['dep-prod', 'node-01'])
  })
})
