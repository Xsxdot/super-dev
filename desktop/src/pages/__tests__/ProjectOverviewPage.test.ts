import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/agent'
import ProjectOverviewPage from '@/pages/ProjectOverviewPage.vue'
import { useAgentStore } from '@/stores/agent'
import { installTestI18n } from '@/test-utils/i18n'

const route = { params: { id: 'p1' } }
const push = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push }),
}))

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      listProjects: vi.fn(),
      listHosts: vi.fn().mockResolvedValue([]),
    },
  }
})

vi.mock('@/components/Overview/RuntimeStatusTab.vue', () => ({
  default: { template: '<div data-test="runtime-tab">runtime</div>' },
}))

vi.mock('@/components/Overview/PipelinesTab.vue', () => ({
  default: { template: '<div data-test="pipelines-tab">pipelines</div>' },
}))

vi.mock('@/components/Overview/ProjectIngressTab.vue', () => ({
  default: { template: '<div data-test="project-ingress-tab">ingress</div>' },
}))

describe('ProjectOverviewPage', () => {
  beforeEach(() => {
    route.params.id = 'p1'
    push.mockClear()
    vi.mocked(api.listProjects).mockReset()
    vi.mocked(api.listProjects).mockResolvedValue([])
    setActivePinia(createPinia())
  })

  it('renders project name and runtime status tab by default', () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'p1',
      name: 'demo',
      root_path: '/tmp/demo',
      services: [],
      pipelines: [],
      environments: [],
    }]

    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.text()).toContain('demo')
    expect(wrapper.find('[data-test="runtime-tab"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipelines-tab"]').exists()).toBe(false)
  })

  it('does not render standalone back chrome in the project overview first viewport', () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'p1',
      name: 'demo',
      root_path: '/tmp/demo',
      services: [],
      pipelines: [],
      environments: [],
    }]

    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.find('[data-test="overview-back"]').exists()).toBe(false)
    expect(push).not.toHaveBeenCalled()
  })

  it('switches to pipelines tab', async () => {
    const agent = useAgentStore()
    agent.projects = [{ id: 'p1', name: 'demo', root_path: '/tmp/demo', services: [] }]
    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })

    await wrapper.find('[data-test="overview-tab-pipelines"]').trigger('click')

    expect(wrapper.find('[data-test="pipelines-tab"]').exists()).toBe(true)
  })

  it('switches to project ingress tab', async () => {
    const agent = useAgentStore()
    agent.projects = [{ id: 'p1', name: 'demo', root_path: '/tmp/demo', services: [] }]
    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    await wrapper.find('[data-test="overview-tab-ingress"]').trigger('click')

    expect(wrapper.find('[data-test="project-ingress-tab"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('入口配置')
  })

  it('switches to project config as a top-level overview tab', async () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'p1',
      name: 'demo',
      root_path: '/tmp/demo',
      services: [{ id: 's1', project_id: 'p1', name: 'web', status: '', required: false, order: 0, deployments: [], debug_credentials: [] }],
      pipelines: [],
      environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    }]

    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect(wrapper.find('[data-test="overview-edit-config"]').exists()).toBe(false)
    await wrapper.find('[data-test="overview-tab-config"]').trigger('click')

    expect(wrapper.find('[data-test="project-config-surface"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="env-settings"]').text()).toContain('设为开发环境')
  })

  it('loads projects when opened directly by route', async () => {
    vi.mocked(api.listProjects).mockResolvedValue([{
      id: 'p1',
      name: 'demo',
      root_path: '/tmp/demo',
      services: [],
      pipelines: [],
      environments: [],
    }])

    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })
    await flushPromises()

    expect(api.listProjects).toHaveBeenCalled()
    expect(wrapper.text()).toContain('demo')
    expect(wrapper.find('[data-test="runtime-tab"]').exists()).toBe(true)
  })

  it('shows missing project state for unknown route id', () => {
    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.text()).toContain('Project not found')
  })
})
