import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import PipelinesTab from '../PipelinesTab.vue'
import pipelinesTabSource from '../PipelinesTab.vue?raw'
import pipelineRowSource from '../PipelineRow.vue?raw'
import type { Project, Run } from '@/api/agent'
import { api } from '@/api/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

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
    props: ['initialMode', 'pipelineId'],
    template: '<div data-test="pipeline-editor" :data-mode="initialMode" :data-pipeline-id="pipelineId"><button data-test="editor-cancel" @click="$emit(`cancel`)">cancel</button></div>',
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
    finished_at: 11000,
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

  it('renders console table without the removed overview panel and shows default history', async () => {
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    expect(wrapper.text()).toContain('Deploy Dev')
    await new Promise(r => setTimeout(r))
    expect(wrapper.find('[data-test="pipeline-console-summary"]').text()).toContain('1')
    expect(wrapper.find('[data-test="pipeline-stat-total"]').text()).toContain('1')
    expect(wrapper.find('[data-test="pipeline-stat-success"]').text()).toContain('1')
    expect(wrapper.find('[data-test="pipeline-refresh"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-table-scroll"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-table-head"]').text()).toContain('流水线')
    expect(wrapper.find('[data-test="pipeline-overview"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-artifact-download"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-latest-version"]').text()).toContain('v1')
    expect(wrapper.find('[data-test="pipeline-latest-duration"]').text()).toContain('10s')
    expect(wrapper.find('[data-test="pipeline-timezone"]').text()).toContain('Asia/Shanghai')

    expect(api.listProjectPipelineRuns).toHaveBeenCalledWith('p1', 'deploy-dev')
    expect(wrapper.find('[data-test="run-history"]').text()).toContain('v1')
    expect(wrapper.find('[data-test="run-history-timeline"]').exists()).toBe(true)
  })

  it('centers icon-only controls through explicit layout styles', () => {
    expect(pipelinesTabSource).toMatch(/\.pipeline-refresh-btn\s*{[^}]*display:\s*inline-grid;[^}]*place-items:\s*center;/s)
    expect(pipelineRowSource).toMatch(/\.icon-btn\s*{[^}]*display:\s*inline-grid;[^}]*place-items:\s*center;/s)
  })

  it('uses a flexible pre-action column so pipeline actions align to the right edge', () => {
    expect(pipelinesTabSource).toContain('--pipeline-actions-width: 126px;')
    expect(pipelinesTabSource).toMatch(/grid-template-columns:\s*var\(--pipeline-name-width\) var\(--pipeline-services-width\) 112px var\(--pipeline-version-width\) 72px minmax\(140px,\s*1fr\) var\(--pipeline-actions-width\);/)
    expect(pipelineRowSource).toMatch(/grid-template-columns:\s*44px var\(--pipeline-name-width\) var\(--pipeline-services-width\) 112px var\(--pipeline-version-width\) 72px minmax\(140px,\s*1fr\) var\(--pipeline-actions-width\);/)
  })

  it('keeps the pipeline name, services, and latest version columns readable', () => {
    expect(pipelinesTabSource).toContain('--pipeline-name-width: 360px;')
    expect(pipelinesTabSource).toContain('--pipeline-services-width: 280px;')
    expect(pipelinesTabSource).toContain('--pipeline-version-width: 176px;')
  })

  it('limits expanded history to recent records and exposes the full history action', async () => {
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({
      items: Array.from({ length: 12 }, (_, index) => run({
        id: `run-${index}`,
        artifact_version: `v${index}`,
        started_at: 1000 + index,
        finished_at: 11000 + index,
        status: index > 4 ? 'failed' : 'success',
      })),
    })
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))

    expect(wrapper.findAll('[data-test="run-history-row"]')).toHaveLength(5)
    expect(wrapper.find('[data-test="run-history-view-all"]').text()).toContain('12')
  })

  it('refreshes all pipeline runs from the dashboard action', async () => {
    const p = project()
    p.pipelines = [
      { id: 'deploy-dev', name: 'Deploy Dev', services: ['api'], artifact_kind: 'file', pipeline: {} },
      { id: 'deploy-prod', name: 'Deploy Prod', services: ['api'], artifact_kind: 'file', pipeline: {} },
    ]
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({ items: [run()] })
    const wrapper = mount(PipelinesTab, { props: { project: p }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))
    vi.mocked(api.listProjectPipelineRuns).mockClear()

    await wrapper.find('[data-test="pipeline-refresh"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.listProjectPipelineRuns).toHaveBeenCalledWith('p1', 'deploy-dev')
    expect(api.listProjectPipelineRuns).toHaveBeenCalledWith('p1', 'deploy-prod')
  })

  it('isolates run history by project when pipeline ids match', async () => {
    const firstProject = project()
    const secondProject: Project = {
      ...project(),
      id: 'p2',
      name: 'demo 2',
      services: [{ id: 'svc-api-2', project_id: 'p2', name: 'api', status: '', required: true, order: 0, deployments: [] }],
    }
    vi.mocked(api.listProjectPipelineRuns).mockImplementation(async (projectId) => ({
      items: [run({
        id: projectId === 'p1' ? 'run-p1' : 'run-p2',
        project_id: projectId,
        artifact_version: projectId === 'p1' ? 'v-p1' : 'v-p2',
      })],
    }))
    const wrapper = mount(PipelinesTab, { props: { project: firstProject }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="run-history"]').text()).toContain('v-p1')

    await wrapper.setProps({ project: secondProject })
    await new Promise(r => setTimeout(r))

    expect(api.listProjectPipelineRuns).toHaveBeenCalledWith('p2', 'deploy-dev')
    expect(wrapper.find('[data-test="run-history"]').text()).toContain('v-p2')
    expect(wrapper.find('[data-test="run-history"]').text()).not.toContain('v-p1')
  })

  it('shows failed pipeline count in console summary', async () => {
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({
      items: [run({ id: 'run-failed', status: 'failed', artifact_version: 'v2' })],
    })
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="pipeline-console-summary"]').text()).toContain('1')
    expect(wrapper.find('[data-test="pipeline-status"]').text()).toContain('失败')
  })

  it('单环境流水线运行时自动选中唯一环境且不显示下拉', async () => {
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-live', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="pipeline-run"]').trigger('click')
    expect(wrapper.find('[data-test="deploy-env-select"]').exists()).toBe(false)
    await wrapper.find('[data-test="deploy-confirm"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({ env_name: 'dev' }))
    const workspace = useWorkspaceStore()
    expect(workspace.activeTab?.type).toBe('run')
    expect(workspace.activeTab?.id).toBe('run:run-live')
  })

  it('多环境流水线运行时必须先选环境，确认按钮初始禁用', async () => {
    const p = project()
    p.environments = [
      { id: 'env-dev', name: 'dev', is_dev: true, order: 0 },
      { id: 'env-prod', name: 'prod', is_dev: false, order: 1 },
    ]
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-multi', env_name: 'prod', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: p }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="pipeline-run"]').trigger('click')

    const select = wrapper.find('[data-test="deploy-env-select"]')
    expect(select.exists()).toBe(true)
    expect(wrapper.find('[data-test="deploy-confirm"]').attributes('disabled')).toBeDefined()

    await select.setValue('prod')
    expect(wrapper.find('[data-test="deploy-confirm"]').attributes('disabled')).toBeUndefined()
    await wrapper.find('[data-test="deploy-confirm"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({ env_name: 'prod' }))
  })

  it('无声明环境的流水线运行时不显示下拉并走默认环境兜底', async () => {
    const p = project()
    p.environments = []
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-fallback', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: p }, global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="pipeline-run"]').trigger('click')
    expect(wrapper.find('[data-test="deploy-env-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="deploy-confirm"]').attributes('disabled')).toBeUndefined()
    await wrapper.find('[data-test="deploy-confirm"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({ env_name: 'dev' }))
  })

  it('shows running run and re-enters live console from the row', async () => {
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({ items: [run({ id: 'run-live', status: 'running' })] })
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="pipeline-running"]').exists()).toBe(true)
    await wrapper.find('[data-test="pipeline-running"]').trigger('click')

    const workspace = useWorkspaceStore()
    expect(workspace.activeTab?.type).toBe('run')
    expect(workspace.activeTab?.id).toBe('run:run-live')
  })

  it('rolls back by reusing deploy path with artifact version', async () => {
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-rollback', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="run-rollback"]').trigger('click')
    expect(wrapper.find('[data-test="deploy-env-select"]').exists()).toBe(false)
    await wrapper.find('[data-test="deploy-confirm"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({
      env_name: 'dev',
      artifact_version: 'v1',
    }))
  })

  it('promotes successful run to target environment through deploy path', async () => {
    const p = project()
    p.environments = [
      { id: 'env-dev', name: 'dev', is_dev: true, order: 0 },
      { id: 'env-prod', name: 'prod', is_dev: false, order: 1 },
    ]
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({
      items: [run({ id: 'run-success', env_name: 'dev', artifact_version: 'v1', status: 'success' })],
    })
    vi.mocked(api.deployProjectPipeline).mockResolvedValue(run({ id: 'run-promote', env_name: 'prod', status: 'running' }))
    const wrapper = mount(PipelinesTab, { props: { project: p }, global: { plugins: [installTestI18n()] } })
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="promote-run-success-prod"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-dev', expect.objectContaining({
      env_name: 'prod',
      artifact_version: 'v1',
    }))
    const workspace = useWorkspaceStore()
    expect(workspace.activeTab?.id).toBe('run:run-promote')
  })

  it('shows failed step summary in expanded history', async () => {
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({
      items: [run({
        id: 'run-failed',
        status: 'failed',
        artifact_version: 'v2',
        step_runs: [
          { step_name: 'Deploy', type: 'include', phase: 'deploy', status: 'failed', tasks: [] },
        ],
      })],
    })
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="run-failed-summary"]').text()).toContain('Deploy')
  })

  it('opens existing ProjectPipelineEditor for edit', async () => {
    const wrapper = mount(PipelinesTab, { props: { project: project() }, global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="pipeline-edit"]').trigger('click')

    expect(wrapper.find('[data-test="pipeline-editor"]').exists()).toBe(true)
  })

  it('passes selected pipeline id to editor', async () => {
    const p = project()
    p.pipelines = [
      { id: 'deploy-dev', name: 'Deploy Dev', services: ['api'], artifact_kind: 'file', pipeline: {} },
      { id: 'deploy-prod', name: 'Deploy Prod', services: ['api'], artifact_kind: 'file', pipeline: {} },
    ]
    vi.mocked(api.listProjectPipelineRuns).mockResolvedValue({ items: [] })
    const wrapper = mount(PipelinesTab, { props: { project: p }, global: { plugins: [installTestI18n()] } })

    await wrapper.findAll('[data-test="pipeline-edit"]')[1].trigger('click')

    expect(wrapper.find('[data-test="pipeline-editor"]').attributes('data-pipeline-id')).toBe('deploy-prod')
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
