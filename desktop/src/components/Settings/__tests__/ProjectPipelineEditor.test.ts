import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { PipelineTemplateSummary, Project } from '@/api/agent'

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

const buildTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'vue-go-combined',
  name: 'Vue + Go 组合构建',
  category: 'build',
  version: '1.0.0',
  digest: 'sha256:build',
  description: '构建 Vue 前端和 Go 后端',
  inputs: {
    frontend_dir: { label: '前端目录 / Frontend directory', type: 'path', required: true, default: '${workspace}/admin' },
  },
}

const deployTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'systemd-seamless',
  name: 'Systemd 无缝部署',
  category: 'deploy',
  version: '1.0.0',
  digest: 'sha256:deploy',
  description: '上传产物并切换服务',
  inputs: {
    role: { label: '目标机器 / Target machines', type: 'target_role', required: true },
  },
}

function projectWithPipeline(): Project {
  const p = project()
  p.services.push({
    id: 's2',
    project_id: 'p1',
    name: 'server',
    status: '',
    required: false,
    order: 1,
    deployments: [],
  })
  p.pipelines = [{
    id: 'deploy-server-admin-prod',
    name: 'Deploy Server Admin Prod',
    services: ['web', 'server'],
    artifact_kind: 'file',
    roles: { build_0_runner: { hosts: ['h1'] }, deploy_1_targets: { hosts: ['h1'] } },
    pipeline: {
      roles: { deploy_1_targets: ['h1'] },
      build: [{
        name: 'Vue + Go 组合构建',
        type: 'include',
        with: {
          template: 'builtin://vue-go-combined',
          version: '1.0.0',
          digest: 'sha256:build',
          vars: { frontend_dir: '${workspace}/admin' },
        },
      }],
      deploy: [{
        name: 'Systemd 无缝部署',
        type: 'include',
        with: {
          template: 'builtin://systemd-seamless',
          version: '1.0.0',
          digest: 'sha256:deploy',
          vars: { role: 'deploy_1_targets' },
        },
      }],
    },
  }]
  return p
}

function projectWithUnevenPhaseCounts(): Project {
  const p = projectWithPipeline()
  const pipeline = p.pipelines![0]
  pipeline.pipeline = {
    ...pipeline.pipeline,
    build: [
      ...(pipeline.pipeline?.build ?? []),
      {
        name: 'Vue + Go 组合构建 Extra',
        type: 'include',
        with: {
          template: 'builtin://vue-go-combined',
          version: '1.0.0',
          digest: 'sha256:build-extra',
          vars: { frontend_dir: '${workspace}/admin-extra' },
        },
      },
    ],
    deploy: [],
    finally: [{
      name: 'Systemd 清理',
      type: 'include',
      with: {
        template: 'builtin://systemd-seamless',
        version: '1.0.0',
        digest: 'sha256:cleanup',
        vars: {},
      },
    }],
  }
  return p
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

  it('uses shared settings wide modal shell', async () => {
    const wrapper = mountProjectPipelineEditor()
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('.settings-modal-backdrop').exists()).toBe(true)
    expect(wrapper.find('.settings-modal-wide').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-config-save"]').classes()).toContain('settings-btn-primary')
    expect(wrapper.find('[data-test="pipeline-config-cancel"]').classes()).toContain('settings-btn')
  })

  it('渲染项目流水线编辑弹窗', async () => {
    const wrapper = mountProjectPipelineEditor()
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).toContain('编辑流水线 · Deploy')
    expect(wrapper.find('[data-test="pipeline-editor-yaml"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-editor-close"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-editor-shell"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-editor-scroll"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="single-pipeline-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="single-pipeline-form-topbar"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-editor-structure"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-preview-open"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-editor-preview"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-config-save-template"]').classes()).toContain('settings-btn-primary')
  })

  it('编辑已有流水线时渲染可操作的阶段编排区和模板输入区', async () => {
    const wrapper = mount(ProjectPipelineEditor, {
      props: {
        project: projectWithPipeline(),
        pipelineId: 'deploy-server-admin-prod',
        pipelineTemplates: [buildTemplate, deployTemplate],
      },
      global: { plugins: [installTestI18n()] },
    })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="pipeline-editor-form-column"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-editor-stage-area"]').text()).toContain('Vue + Go 组合构建')
    expect(wrapper.find('[data-test="pipeline-editor-stage-area"]').text()).toContain('Systemd 无缝部署')
    expect(wrapper.find('[data-test="pipeline-wizard-detail"]').text()).toContain('模板输入')
    expect(wrapper.find('[data-test="pipeline-preview-open"]').attributes('data-preview-count')).toBe('2')
    expect(wrapper.find('[data-test="pipeline-editor-preview-strip"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="pipeline-editor-preview-node"]')).toHaveLength(0)
    expect(wrapper.find('[data-test="wizard-preview-strip"]').exists()).toBe(false)
  })

  it('左侧结构 rail 展示当前流水线真实阶段模板数量', async () => {
    const wrapper = mount(ProjectPipelineEditor, {
      props: {
        project: projectWithUnevenPhaseCounts(),
        pipelineId: 'deploy-server-admin-prod',
        pipelineTemplates: [buildTemplate, deployTemplate],
      },
      global: { plugins: [installTestI18n()] },
    })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="pipeline-editor-rail-build"]').text()).toContain('2 模板')
    expect(wrapper.find('[data-test="pipeline-editor-rail-deploy"]').text()).toContain('0 模板')
    expect(wrapper.find('[data-test="pipeline-editor-rail-finally"]').text()).toContain('1 模板')
    expect(wrapper.find('[data-test="pipeline-editor-rail-build"] .rail-icon svg').exists()).toBe(true)
  })

  it('编辑已有流水线时优先展示后端展开后的预览步骤', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.previewProjectPipeline).mockResolvedValueOnce({
      run: {
        deployment_id: 'project:p1:pipeline:deploy-server-admin-prod:env:dev',
        status: 'pending',
        step_runs: [
          { step_name: 'Vue + Go 组合构建.Build Frontend', type: 'local_command', phase: 'build', status: 'pending', tasks: [] },
          { step_name: 'Vue + Go 组合构建.Build Backend', type: 'local_command', phase: 'build', status: 'pending', tasks: [] },
          { step_name: 'Vue + Go 组合构建.Package', type: 'local_command', phase: 'build', status: 'pending', tasks: [] },
          { step_name: 'Systemd 无缝部署.Prepare', type: 'remote_command', phase: 'deploy', status: 'pending', tasks: [{ host_id: 'h1', host_name: 'ali-01', status: 'pending' }] },
          { step_name: 'Systemd 无缝部署.Upload', type: 'remote_command', phase: 'deploy', status: 'pending', tasks: [{ host_id: 'h1', host_name: 'ali-01', status: 'pending' }] },
          { step_name: 'Systemd 无缝部署.Restart', type: 'remote_command', phase: 'deploy', status: 'pending', tasks: [{ host_id: 'h1', host_name: 'ali-01', status: 'pending' }] },
          { step_name: 'Health Check', type: 'http_check', phase: 'finally', status: 'pending', tasks: [] },
        ],
      },
    })

    const wrapper = mount(ProjectPipelineEditor, {
      props: {
        project: projectWithPipeline(),
        pipelineId: 'deploy-server-admin-prod',
        pipelineTemplates: [buildTemplate, deployTemplate],
      },
      global: { plugins: [installTestI18n()] },
    })
    await flushPromises()

    expect(api.previewProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-server-admin-prod', expect.objectContaining({
      env_name: 'dev',
      service_names: ['web', 'server'],
    }))
    expect(wrapper.find('[data-test="pipeline-preview-open"]').attributes('data-preview-count')).toBe('5')
    expect(wrapper.find('[data-test="pipeline-editor-preview-strip"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="pipeline-editor-preview-node"]')).toHaveLength(0)
  })

  it('点击预览按钮时刷新并渲染全屏执行图', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.previewProjectPipeline).mockResolvedValue({
      run: {
        deployment_id: 'project:p1:pipeline:deploy-server-admin-prod:env:dev',
        status: 'pending',
        step_runs: [
          { step_name: 'Vue + Go 组合构建.Build Frontend', type: 'local_command', phase: 'build', status: 'pending', tasks: [] },
          { step_name: 'Vue + Go 组合构建.Build Backend', type: 'local_command', phase: 'build', status: 'pending', tasks: [] },
          { step_name: 'Systemd 无缝部署.Restart', type: 'remote_command', phase: 'deploy', status: 'pending', tasks: [{ host_id: 'h1', host_name: 'ali-01', status: 'pending' }] },
        ],
      },
    })
    const wrapper = mount(ProjectPipelineEditor, {
      props: {
        project: projectWithPipeline(),
        pipelineId: 'deploy-server-admin-prod',
        pipelineTemplates: [buildTemplate, deployTemplate],
      },
      global: { plugins: [installTestI18n()] },
    })
    await flushPromises()
    vi.mocked(api.previewProjectPipeline).mockClear()

    await wrapper.find('[data-test="pipeline-preview-open"]').trigger('click')
    await flushPromises()

    expect(api.previewProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-server-admin-prod', expect.objectContaining({
      env_name: 'dev',
      service_names: ['web', 'server'],
    }))
    expect(wrapper.find('[data-test="pipeline-preview-overlay"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-preview-flow"]').text()).toContain('Build Frontend')
    expect(wrapper.find('[data-test="pipeline-preview-flow"]').text()).toContain('Deploy')
    expect(wrapper.find('[data-test="pipeline-preview-flow"]').text()).toContain('ali-01')

    await wrapper.find('[data-test="pipeline-preview-close"]').trigger('click')
    expect(wrapper.find('[data-test="pipeline-preview-overlay"]').exists()).toBe(false)
  })

  it('Esc 关闭预览弹层', async () => {
    const wrapper = mount(ProjectPipelineEditor, {
      props: {
        project: projectWithPipeline(),
        pipelineId: 'deploy-server-admin-prod',
        pipelineTemplates: [buildTemplate, deployTemplate],
      },
      global: { plugins: [installTestI18n()] },
    })
    await flushPromises()

    await wrapper.find('[data-test="pipeline-preview-open"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="pipeline-preview-overlay"]').exists()).toBe(true)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()

    expect(wrapper.find('[data-test="pipeline-preview-overlay"]').exists()).toBe(false)
  })

  it('保存项目级 pipeline 并保留非流水线配置', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mountProjectPipelineEditor()
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="single-pipeline-name"]').setValue('Deploy Dev')
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
