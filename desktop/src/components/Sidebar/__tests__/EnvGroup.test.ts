import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EnvGroup from '@/components/Sidebar/EnvGroup.vue'
import { useAgentStore } from '@/stores/agent'
import type { Deployment, Service } from '@/api/agent'

const makeService = (id: string, name: string, envName: string, depExtra: Partial<Deployment> = {}): Service => ({
  id,
  project_id: 'proj-1',
  name,
  required: false,
  order: 0,
  status: '',
  deployments: [{ id: 'dep-' + id, env_name: envName, location: 'local', status: '', ...depExtra }],
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

  it('点击 service 行 emit open-deployment（携带本 env 的 deploymentId）', async () => {
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
    })

    await wrapper.find('[data-test="env-service-row"]').trigger('mouseenter')
    expect(wrapper.find('[data-test="row-restart"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-stop"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="row-start"]').exists()).toBe(false)
  })
})
