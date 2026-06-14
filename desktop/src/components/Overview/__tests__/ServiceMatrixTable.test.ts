/**
 * ServiceMatrixTable tests.
 *
 * Responsibilities:
 *   - Verify service matrix rows render aggregated primary environment health
 *   - Verify selecting a service is a row-level action
 *
 * Boundaries:
 *   - Does not test runtime polling
 *   - Does not render selected service node detail
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ServiceMatrixTable from '../ServiceMatrixTable.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { ServiceMatrix } from '@/lib/runtimeServiceMatrix'

function matrix(): ServiceMatrix {
  return {
    environments: ['prod'],
    devEnvironments: ['dev'],
    preferredServiceId: 'svc-server',
    localDev: { healthy: 0, total: 1, instances: 1 },
    kpis: {
      critical: 1,
      services: 2,
      instances: 5,
      envs: [{ envName: 'prod', healthy: 4, total: 5 }],
    },
    rows: [
      {
        serviceId: 'svc-server',
        serviceName: 'server',
        total: 5,
        abnormal: 1,
        cpuPercent: 2.5,
        memBytes: 512 * 1024 * 1024,
        instances: [],
        nodeHealths: [
          { nodeId: 'n3', nodeName: 'node-03', envName: 'prod', health: 'stopped' },
          { nodeId: 'n1', nodeName: 'node-01', envName: 'prod', health: 'running' },
          { nodeId: 'n2', nodeName: 'node-02', envName: 'prod', health: 'running' },
        ],
        envs: [
          { envName: 'prod', instances: [], total: 5, healthy: 4, abnormal: 1, debuggingCount: 0, health: 'stopped', label: 'Stopped 4/5' },
        ],
        devEnvs: [
          { envName: 'dev', instances: [], total: 1, healthy: 0, abnormal: 1, debuggingCount: 0, health: 'stopped', label: 'Stopped 0/1' },
        ],
      },
      {
        serviceId: 'svc-audio',
        serviceName: 'audio',
        total: 0,
        abnormal: 0,
        cpuPercent: null,
        memBytes: null,
        instances: [],
        nodeHealths: [],
        envs: [
          { envName: 'prod', instances: [], total: 0, healthy: 0, abnormal: 0, debuggingCount: 0, health: 'not_configured', label: 'Not configured' },
        ],
        devEnvs: [],
      },
    ],
  }
}

describe('ServiceMatrixTable', () => {
  it('renders primary environment columns, service rows, node beads, and metrics', () => {
    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: matrix(), selectedServiceId: 'svc-server' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('Service Matrix')
    expect(wrapper.find('[data-test="matrix-env-prod"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="matrix-env-dev"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('server')
    expect(wrapper.text()).toContain('Stopped 4/5')
    expect(wrapper.text()).toContain('2.5%')
    expect(wrapper.text()).toContain('512 MiB')
    expect(wrapper.findAll('.node-bead')).toHaveLength(3)
  })

  it('emits select-service when a service row is clicked', async () => {
    const wrapper = mount(ServiceMatrixTable, {
      props: { matrix: matrix(), selectedServiceId: 'svc-server' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="service-row-svc-audio"]').trigger('click')

    expect(wrapper.emitted('select-service')?.[0]).toEqual(['svc-audio'])
  })
})
