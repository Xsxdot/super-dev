/**
 * NodeCard component tests.
 *
 * Responsibilities:
 *   - Verify a node card renders rich deployment status
 *   - Verify missing metrics degrade without breaking layout
 *   - Verify clicking a deployment row requests log opening
 *
 * Boundaries:
 *   - Does not read Pinia stores
 *   - Does not open workspace tabs
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NodeCard from '../NodeCard.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { NodeCenterNode } from '@/lib/nodeCenter'
import type { RuntimeInstanceStatus } from '@/api/agent'

function instance(partial: Partial<RuntimeInstanceStatus> = {}): RuntimeInstanceStatus {
  return {
    service_id: 'svc-api',
    service_name: 'api',
    env_name: 'prod',
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

function card(overrides: Partial<NodeCenterNode> = {}): NodeCenterNode {
  return {
    hostId: 'host-1',
    name: 'ali-01',
    address: '10.0.0.8',
    reachable: true,
    muted: false,
    agent: {
      installed: true,
      version: '0.1.0',
      health: 'healthy',
      reachable: true,
    },
    deployments: [{ instance: instance(), envName: 'prod', projectName: 'Demo', abnormal: false }],
    serviceCount: 1,
    updatedAt: '2026-06-06T10:00:00Z',
    configured: true,
    ...overrides,
  }
}

describe('NodeCard', () => {
  it('renders node header and rich service metrics', () => {
    const wrapper = mount(NodeCard, {
      props: { node: card() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="node-card-host-1"]').classes()).toContain('is-reachable')
    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('Remote · agent 0.1.0 · healthy · 1 service')
    expect(wrapper.text()).toContain('api')
    expect(wrapper.text()).toContain('Demo · prod')
    expect(wrapper.text()).toContain('12.5%')
    expect(wrapper.text()).toContain('128 MiB')
    expect(wrapper.text()).toContain('1h 1m')
    expect(wrapper.text()).toContain('0')
    expect(wrapper.find('[data-test="cpu-bar-dep-api"]').attributes('style')).toContain('width: 12.5%')
  })

  it('renders disconnected nodes and empty service lists', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          reachable: false,
          muted: true,
          agent: { installed: false, health: 'unknown', reachable: false },
          deployments: [],
          serviceCount: 0,
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="node-card-host-1"]').classes()).toContain('is-muted')
    expect(wrapper.text()).toContain('Disconnected')
    expect(wrapper.text()).toContain('No remote services')
  })

  it('uses dashes and hides the CPU bar when metrics are missing', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          deployments: [{
            envName: undefined,
            abnormal: true,
            instance: instance({
              error: 'process unavailable',
              metrics: {
                cpu_percent: null,
                mem_bytes: null,
                uptime_sec: null,
                restarts: null,
                health: 'unknown',
                base: 'systemd',
              },
            }),
          }],
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).toContain('process unavailable')
    expect(wrapper.find('[data-test="cpu-bar-dep-api"]').exists()).toBe(false)
  })

  it('renders abnormal services first based on the provided view model order', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          serviceCount: 2,
          deployments: [
            {
              abnormal: true,
              envName: 'prod',
              instance: instance({
                service_name: 'worker',
                deployment_id: 'dep-worker',
                metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: 3, health: 'failed', base: 'systemd' },
              }),
            },
            { abnormal: false, envName: 'prod', instance: instance() },
          ],
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const rows = wrapper.findAll('[data-test^="node-service-"]')
    expect(rows.map(row => row.text())).toEqual(expect.arrayContaining([
      expect.stringContaining('worker'),
      expect.stringContaining('api'),
    ]))
    expect(rows[0].text()).toContain('worker')
  })

  it('renders project and environment as secondary service metadata', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          deployments: [{
            abnormal: false,
            envName: 'prod',
            projectName: 'Billing API',
            instance: instance({
              service_name: 'server',
              deployment_id: 'dep-billing-server',
            }),
          }],
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const row = wrapper.find('[data-test="node-service-dep-billing-server"]')
    expect(row.find('[data-test="service-name"]').text()).toBe('server')
    expect(row.find('[data-test="service-context"]').text()).toBe('Billing API · prod')
  })

  it('emits open logs with deployment and node ids', async () => {
    const wrapper = mount(NodeCard, {
      props: { node: card() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="node-service-dep-api"]').trigger('click')

    expect(wrapper.emitted('open-logs')?.[0]).toEqual(['dep-api', 'host-1'])
  })

  it('renders current route and degraded marker', () => {
    const wrapper = mount(NodeCard, {
      props: { node: card({ route: { selectedIndex: 1, selectedType: 'tunnel', degraded: true } }) },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="node-route-badge"]').text()).toBe('Tunnel · degraded')
  })

  it('renders the selected connection type as a localized badge beside the node name', () => {
    const wrapper = mount(NodeCard, {
      props: { node: card({ route: { selectedIndex: 0, selectedType: 'direct', degraded: false } }) },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const titleRow = wrapper.find('.node-title-row')
    expect(titleRow.text()).toContain('ali-01')
    expect(titleRow.find('[data-test="node-route-badge"]').text()).toBe('直连')
    expect(wrapper.text()).not.toContain('via direct')
  })
})
