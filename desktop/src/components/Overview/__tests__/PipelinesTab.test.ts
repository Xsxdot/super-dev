import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import PipelinesTab from '../PipelinesTab.vue'
import type { Project, Run } from '@/api/agent'
import { api } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'

const push = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
}))

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      listProjectPipelineRuns: vi.fn(),
      deployProjectPipeline: vi.fn(),
      listPipelineTemplates: vi.fn().mockResolvedValue({ items: [] }),
    },
  }
})

vi.mock('@/components/Settings/ProjectPipelineEditor.vue', () => ({
  default: {
    props: ['initialMode'],
    template: '<div data-test="pipeline-editor" :data-mode="initialMode"><button data-test="editor-cancel" @click="$emit(`cancel`)">cancel</button></div>',
  },
}))

function project(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    services: [{ id: 'svc-api', project_id: 'p1', name: 'api', status: '', required: true, order: 0, deployments: [] }],
    environments: [{ id: 'env-dev', name: 'dev', is_dev: true, order: 0 }],
    pipelines: [{ id: 'deploy-dev', name: 'Deploy Dev', services: ['api'], artifact_kind: 'file', pipeline: {} }],
  }
}

function emptyProject(): Project {
  return { ...project(), pipelines: [] }
}

function run(partial: Partial<Run> = {}): Run {
  return {
    id: 'run-1',
    project_id: 'p1',
    pipeline_id: 'deploy-dev',
    env_name: 'dev',
    deployment_id: '',
    artifact_version: 'v1',
    status: 'success',
    step_runs: [],
    started_at: 1000,
    finished_at: 2000,
    ...partial,
  }
}

describe('PipelinesTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({ items: [run()] })
    vi.mocked(api.listPipelineTemplates).mockResolvedValue({ items: [] })
  })

  it('renders pipeline row and loads history when expanded', async () => {
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    expect(wrapper.text()).toContain('Deploy Dev')
    await wrapper.find('[data-test="pipeline-expand"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.listProjectPipelineRuns).toHaveBeenCalledWith('p1', 'deploy-dev')
    expect(wrapper.text()).toContain('v1')
  })

  it('deploys selected environment and navigates to live console', async () => {
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-live', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="pipeline-run"]').trigger('click')
    await wrapper.find('[data-test="deploy-confirm"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({ env_name: 'dev' }))
    expect(push).toHaveBeenCalledWith('/project/p1/pipelines/deploy-dev/runs/run-live?mode=live')
  })

  it('rolls back by reusing deploy path with artifact version', async () => {
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-rollback', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="pipeline-expand"]').trigger('click')
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="run-rollback"]').trigger('click')
    await wrapper.find('[data-test="deploy-confirm"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({
      env_name: 'dev',
      artifact_version: 'v1',
    }))
  })

  it('opens existing ProjectPipelineEditor for edit', async () => {
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="pipeline-edit"]').trigger('click')

    expect(wrapper.find('[data-test="pipeline-editor"]').exists()).toBe(true)
  })

  it('空流水线时展示模板和空白创建入口', async () => {
    const wrapper = mount(PipelinesTab, { props: { project: emptyProject() }, global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="pipeline-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-create-from-template"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-create-blank"]').exists()).toBe(true)

    await wrapper.find('[data-test="pipeline-create-from-template"]').trigger('click')
    expect(wrapper.find('[data-test="pipeline-editor"]').attributes('data-mode')).toBe('template')
  })
})
