import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ProjectOverviewPage from '@/pages/ProjectOverviewPage.vue'
import { useAgentStore } from '@/stores/agent'
import { installTestI18n } from '@/test-utils/i18n'

const route = { params: { id: 'p1' } }
const push = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push }),
}))

vi.mock('@/components/Overview/RuntimeStatusTab.vue', () => ({
  default: { template: '<div data-test="runtime-tab">runtime</div>' },
}))

vi.mock('@/components/Overview/PipelinesTab.vue', () => ({
  default: { template: '<div data-test="pipelines-tab">pipelines</div>' },
}))

describe('ProjectOverviewPage', () => {
  beforeEach(() => {
    route.params.id = 'p1'
    push.mockClear()
    setActivePinia(createPinia())
  })

  it('renders project name and runtime tab by default', () => {
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
  })

  it('returns to the main workspace from the overview header', async () => {
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

    await wrapper.find('[data-test="overview-back"]').trigger('click')

    expect(push).toHaveBeenCalledWith('/')
  })

  it('switches to pipelines tab', async () => {
    const agent = useAgentStore()
    agent.projects = [{ id: 'p1', name: 'demo', root_path: '/tmp/demo', services: [] }]
    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })

    await wrapper.find('[data-test="overview-tab-pipelines"]').trigger('click')

    expect(wrapper.find('[data-test="pipelines-tab"]').exists()).toBe(true)
  })

  it('shows missing project state for unknown route id', () => {
    const wrapper = mount(ProjectOverviewPage, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.text()).toContain('Project not found')
  })
})
