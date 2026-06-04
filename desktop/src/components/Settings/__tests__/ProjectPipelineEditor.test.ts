import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([{ id: 'h1', name: 'host-1' }]),
      putProjectSetup: vi.fn().mockResolvedValue({ id: 'p1' }),
      listProjects: vi.fn().mockResolvedValue([]),
      previewProjectPipeline: vi.fn().mockResolvedValue({
        run: {
          deployment_id: 'project:p1:pipeline:pipeline-1:env:dev',
          status: 'pending',
          step_runs: [{ step_name: 'Deploy', type: 'remote_command', phase: 'deploy', status: 'pending', tasks: [] }],
        },
      }),
      getPipelineTemplate: vi.fn().mockResolvedValue({
        source: 'builtin',
        id: 'systemd',
        version: '1.0.0',
        digest: 'sha256:systemd',
        yaml: 'id: systemd\n',
        template: { id: 'systemd', name: 'Systemd', version: '1.0.0', inputs: {}, steps: [] },
      }),
    },
  }
})

function project(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    variables: { app_name: 'demo' },
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    services: [{
      id: 's1',
      project_id: 'p1',
      name: 'web',
      status: '',
      required: false,
      order: 0,
      deployments: [],
    }],
    pipelines: [],
  }
}

function mountProjectPipelineEditor(locale: 'zh-CN' | 'en-US' = 'zh-CN') {
  return mount(ProjectPipelineEditor, {
    props: { project: project(), pipelineTemplates: [] },
    global: { plugins: [installTestI18n(locale)] },
  })
}

describe('ProjectPipelineEditor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('渲染项目流水线编辑弹窗', async () => {
    const wrapper = mountProjectPipelineEditor()
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).toContain('编辑流水线 · demo')
    expect(wrapper.find('[data-test="add-project-pipeline"]').exists()).toBe(true)
  })

  it('保存项目级 pipeline 并保留非流水线配置', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mountProjectPipelineEditor()
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="add-project-pipeline"]').trigger('click')
    await wrapper.find('[data-test="project-pipeline-name"]').setValue('Deploy Dev')
    await wrapper.find('[data-test="pipeline-config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      variables: { app_name: 'demo' },
      environments: expect.any(Array),
      services: expect.any(Array),
      pipelines: [expect.objectContaining({ name: 'Deploy Dev' })],
    }))
    expect(wrapper.emitted('saved')).toBeTruthy()
  })

  it('点击取消 emit cancel', async () => {
    const wrapper = mountProjectPipelineEditor()
    await wrapper.find('[data-test="pipeline-config-cancel"]').trigger('click')

    expect(wrapper.emitted('cancel')).toBeTruthy()
  })

  it('英文 locale 下渲染保存和取消按钮', async () => {
    const wrapper = mountProjectPipelineEditor('en-US')

    expect(wrapper.text()).toContain('Save')
    expect(wrapper.text()).toContain('Cancel')
  })

  it('从项目上下文查看模板时可预览并保存套用结果', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mount(ProjectPipelineEditor, {
      props: {
        project: project(),
        pipelineTemplates: [{ source: 'builtin', id: 'systemd', name: 'Systemd', category: 'deploy', version: '1.0.0', digest: 'sha256:systemd' }],
        initialMode: 'template',
      },
      global: { plugins: [installTestI18n()] },
    })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://systemd@1.0.0')
    await wrapper.find('[data-test="block-0-view-template"]').trigger('click')
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="template-apply"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.previewProjectPipeline).toHaveBeenCalledWith('p1', 'pipeline-1', expect.objectContaining({ env_name: 'dev' }))
    expect(wrapper.text()).toContain('Deploy')
    await wrapper.find('[data-test="pipeline-config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))
    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      pipelines: expect.arrayContaining([
        expect.objectContaining({
          pipeline: expect.objectContaining({
            deploy: expect.arrayContaining([
              expect.objectContaining({
                type: 'include',
                with: expect.objectContaining({ template: 'builtin://systemd', version: '1.0.0' }),
              }),
            ]),
          }),
        }),
      ]),
    }))
  })
})
