import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import ServiceRow from '@/components/Sidebar/ServiceRow.vue'
import type { Service, Deployment, DeployLocation } from '@/api/agent'

function makeDeployment(id: string, envName: string, location: DeployLocation = 'local'): Deployment {
  return {
    id,
    env_name: envName,
    location,
    status: 'running',
  }
}

function makeService(deployments?: Deployment[]): Service {
  return {
    id: 'svc-api',
    project_id: 'project-a',
    name: 'api',
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
    deployments,
  }
}

describe('ServiceRow - no deployments', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('无 deployments 时不渲染子行', () => {
    const wrapper = mount(ServiceRow, {
      props: {
        service: makeService(),
        projectId: 'project-a',
        selected: false,
      },
    })

    expect(wrapper.find('[data-test="deployment-list"]').exists()).toBe(false)
  })

  it('无 deployments 时点击 emit click', async () => {
    const wrapper = mount(ServiceRow, {
      props: {
        service: makeService(),
        projectId: 'project-a',
        selected: false,
      },
    })

    await wrapper.find('.service-row').trigger('click')
    expect(wrapper.emitted('click')).toBeTruthy()
  })
})

describe('ServiceRow - with deployments', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('有 deployments 时展示 deployment 子行', () => {
    const deployments = [
      makeDeployment('dep-1', 'prod', 'remote'),
      makeDeployment('dep-2', 'staging', 'local'),
    ]
    const wrapper = mount(ServiceRow, {
      props: {
        service: makeService(deployments),
        projectId: 'project-a',
        selected: false,
      },
    })

    expect(wrapper.find('[data-test="deployment-list"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-test="deployment-row"]').length).toBe(2)
  })

  it('deployment 子行显示 env_name', () => {
    const deployments = [makeDeployment('dep-1', 'prod', 'remote')]
    const wrapper = mount(ServiceRow, {
      props: {
        service: makeService(deployments),
        projectId: 'project-a',
        selected: false,
      },
    })

    const row = wrapper.find('[data-test="deployment-row"]')
    expect(row.text()).toContain('prod')
  })

  it('点击 deployment 子行 emit open-deployment', async () => {
    const deployments = [makeDeployment('dep-1', 'prod', 'remote')]
    const wrapper = mount(ServiceRow, {
      props: {
        service: makeService(deployments),
        projectId: 'project-a',
        selected: false,
      },
    })

    await wrapper.find('[data-test="deployment-row"]').trigger('click')

    const emitted = wrapper.emitted('open-deployment')
    expect(emitted).toBeTruthy()
    expect(emitted![0][0]).toEqual({ deploymentId: 'dep-1', title: 'api · prod' })
  })

  it('点击 deployment 子行不 emit click', async () => {
    const deployments = [makeDeployment('dep-1', 'prod', 'remote')]
    const wrapper = mount(ServiceRow, {
      props: {
        service: makeService(deployments),
        projectId: 'project-a',
        selected: false,
      },
    })

    await wrapper.find('[data-test="deployment-row"]').trigger('click')

    expect(wrapper.emitted('click')).toBeFalsy()
  })

  it('点击有 deployments 的服务行切换子行显示', async () => {
    const deps = [makeDeployment('dep-1', 'dev', 'local')]
    const wrapper = mount(ServiceRow, {
      props: { service: makeService(deps), projectId: 'proj-1', selected: false },
    })
    // 默认展开，子行可见
    expect(wrapper.find('[data-test="deployment-list"]').exists()).toBe(true)
    // 点击服务行折叠
    await wrapper.find('.service-row').trigger('click')
    expect(wrapper.find('[data-test="deployment-list"]').exists()).toBe(false)
    // 再次点击展开
    await wrapper.find('.service-row').trigger('click')
    expect(wrapper.find('[data-test="deployment-list"]').exists()).toBe(true)
  })
})
