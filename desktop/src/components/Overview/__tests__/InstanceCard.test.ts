import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import InstanceCard from '../InstanceCard.vue'
import type { RuntimeInstanceStatus } from '@/api/agent'

function instance(partial: Partial<RuntimeInstanceStatus> = {}): RuntimeInstanceStatus {
  return {
    service_id: 'svc-api',
    service_name: 'api',
    env_name: 'prod',
    deployment_id: 'dep-api',
    node_id: 'local',
    node_name: 'local',
    is_local: true,
    metrics: {
      cpu_percent: 12.5,
      mem_bytes: 128 * 1024 * 1024,
      uptime_sec: 3661,
      restarts: 0,
      health: 'running',
      base: 'process',
    },
    ...partial,
  }
}

describe('InstanceCard', () => {
  it('renders process metrics and formatted uptime', () => {
    const wrapper = mount(InstanceCard, { props: { instance: instance() } })

    expect(wrapper.findAll('.metric')).toHaveLength(4)
    expect(wrapper.text()).toContain('api')
    expect(wrapper.text()).toContain('12.5%')
    expect(wrapper.text()).toContain('128 MiB')
    expect(wrapper.text()).toContain('1h 1m')
  })

  it('renders running unknown metrics as dashes', () => {
    const wrapper = mount(InstanceCard, {
      props: {
        instance: instance({
          error: 'connection failed',
          metrics: {
            cpu_percent: null,
            mem_bytes: null,
            uptime_sec: null,
            restarts: null,
            health: 'running',
            base: 'systemd',
          },
        }),
      },
    })

    expect(wrapper.text()).toContain('--')
    expect(wrapper.text()).toContain('connection failed')
  })

  it('停止态折叠,不渲染指标列', () => {
    const wrapper = mount(InstanceCard, {
      props: {
        instance: instance({
          metrics: {
            cpu_percent: null,
            mem_bytes: null,
            uptime_sec: null,
            restarts: null,
            health: 'stopped',
            base: 'systemd',
          },
        }),
      },
    })

    expect(wrapper.findAll('.metric')).toHaveLength(0)
  })

  it('始终显示基座 chip', () => {
    const wrapper = mount(InstanceCard, {
      props: {
        instance: instance({
          metrics: {
            cpu_percent: null,
            mem_bytes: null,
            uptime_sec: null,
            restarts: null,
            health: 'stopped',
            base: 'systemd',
          },
        }),
      },
    })

    expect(wrapper.find('.base-chip').text()).toContain('systemd')
  })

  it('emits open logs with deployment and node ids', async () => {
    const wrapper = mount(InstanceCard, { props: { instance: instance() } })

    await wrapper.trigger('click')

    expect(wrapper.emitted('open-logs')?.[0]).toEqual(['dep-api', 'local'])
  })
})
