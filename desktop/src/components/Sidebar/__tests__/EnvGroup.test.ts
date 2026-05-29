import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import EnvGroup from '@/components/Sidebar/EnvGroup.vue'
import type { Service } from '@/api/agent'

const makeService = (id: string, name: string, envName: string): Service => ({
  id,
  project_id: 'proj-1',
  name,
  command: 'go run .',
  work_dir: '/',
  required: false,
  order: 0,
  status: '',
  deployments: [{ id: 'dep-' + id, env_name: envName, location: 'local', status: '' }],
})

describe('EnvGroup', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
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
    })

    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(false)
    await wrapper.find('[data-test="env-group-header"]').trigger('click')
    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(true)
  })

  it('点击 service 行 emit select-service', async () => {
    const wrapper = mount(EnvGroup, {
      props: {
        envName: 'dev',
        isDev: true,
        projectId: 'proj-1',
        services: [makeService('svc-1', 'web', 'dev')],
        selectedServiceIds: new Set<string>(),
      },
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('click')

    const emitted = wrapper.emitted('select-service')
    expect(emitted).toBeTruthy()
    expect((emitted![0][0] as { serviceId: string; projectId: string }).serviceId).toBe('svc-1')
    expect((emitted![0][0] as { serviceId: string; projectId: string }).projectId).toBe('proj-1')
  })
})
