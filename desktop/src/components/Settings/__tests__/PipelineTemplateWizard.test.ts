/**
 * PipelineTemplateWizard 测试模板化流水线组合编辑器。
 *
 * 职责：
 *   - 验证按阶段添加多个模板
 *   - 验证模板不再渲染每模板运行机器选择
 *   - 验证 target_role 输入保存为运行组变量引用
 *   - 验证已有 include pipeline 可回填
 *
 * 边界：
 *   - 不调用真实模板预览接口
 *   - 不解析模板 YAML
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PipelineTemplateWizard from '@/components/Settings/PipelineTemplateWizard.vue'
import type { Pipeline, PipelinePreviewResponse, PipelineTemplateSummary } from '@/api/agent'

const buildTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'go-binary-build',
  category: 'build',
  name: 'Go Build',
  version: '1.0.0',
  digest: 'sha256:build',
  inputs: {
    app_name: { label: '应用名', type: 'string', required: true, description: '应用名' },
  },
}

const deployTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'systemd-seamless-deploy',
  category: 'deploy',
  name: 'Systemd Deploy',
  version: '1.0.0',
  digest: 'sha256:deploy',
  inputs: {
    role: { label: '目标机器', type: 'target_role', required: true, description: '部署目标机器' },
    app_name: { label: '应用名', type: 'string', required: true, description: '应用名' },
  },
}

const packageTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'archive-package',
  category: 'general',
  name: 'Archive Package',
  version: '1.0.0',
  digest: 'sha256:package',
  inputs: {
    artifact: { label: '产物', type: 'path', required: true, description: '最终产物' },
    files: { label: '文件', type: 'file_list', required: true, description: '文件清单' },
  },
}

const importedBuildTemplate: PipelineTemplateSummary = {
  source: 'user',
  id: 'imported-build',
  category: 'build',
  name: 'Imported Build',
  version: '1.0.0',
  digest: 'sha256:imported',
  inputs: {
    app_name: { label: '应用名', type: 'string', required: true, default: 'demo' },
  },
}

const booleanTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'systemd-seamless-deploy',
  category: 'deploy',
  name: 'Systemd Deploy',
  version: '1.0.0',
  digest: 'sha256:boolean',
  inputs: {
    skip_http_check: { label: '跳过 HTTP 检查 / Skip HTTP check', type: 'boolean', required: false, default: 'false' },
  },
}

const firstNoInputTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'first-build',
  category: 'build',
  name: 'First Build',
  version: '1.0.0',
  digest: 'sha256:first',
  inputs: {},
}

const secondNoInputTemplate: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'second-build',
  category: 'build',
  name: 'Second Build',
  version: '1.0.0',
  digest: 'sha256:second',
  inputs: {},
}

describe('PipelineTemplateWizard', () => {
  it('无 pipeline 时展示配置入口', () => {
    const wrapper = mount(PipelineTemplateWizard, { props: { modelValue: undefined, templates: [buildTemplate] } })
    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(true)
  })

  it('initialMode=template 时直接进入模板配置界面', () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: { modelValue: undefined, templates: [buildTemplate], initialMode: 'template' },
    })

    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(false)
    expect(wrapper.find('.pipeline-wizard > template').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-station-base"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="add-template-build"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-phase-tabs"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-wizard-detail"]').exists()).toBe(true)
  })

  it('阶段导航只展示流水线阶段，不再渲染基础信息 slot', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: { modelValue: undefined, templates: [buildTemplate], initialMode: 'template' },
      slots: { base: '<div data-test="base-fields">基础字段</div>' },
    })

    expect(wrapper.find('[data-test="base-fields"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-wizard-canvas"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-phase-tabs"]').findAll('button')).toHaveLength(3)
  })

  it('左栏一次只渲染当前阶段', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate, deployTemplate],
        initialMode: 'template',
      },
    })

    expect(wrapper.findAll('.phase-section')).toHaveLength(1)
    expect(wrapper.find('.phase-section').text()).toContain('构建')
    expect(wrapper.find('.phase-section').text()).not.toContain('部署')
    expect(wrapper.find('[data-test="add-template-deploy"]').exists()).toBe(false)

    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')

    expect(wrapper.findAll('.phase-section')).toHaveLength(1)
    expect(wrapper.find('.phase-section').text()).toContain('部署')
    expect(wrapper.find('.phase-section').text()).not.toContain('构建')
    expect(wrapper.find('[data-test="add-template-build"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="add-template-deploy"]').exists()).toBe(true)
  })

  it('按阶段保存多个模板和运行组变量引用', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate, deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://go-binary-build@1.0.0')
    await wrapper.find('[data-test="block-0-input-app_name"]').setValue('api')
    expect(wrapper.find('[data-test="block-0-help-app_name"]').attributes('title')).toBe('应用名')

    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-1-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')
    await wrapper.find('[data-test="block-1-input-role"]').setValue('api_targets')
    await wrapper.find('[data-test="block-1-input-app_name"]').setValue('api')
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(pipeline.build?.[0].with?.template).toBe('builtin://go-binary-build')
    expect(pipeline.deploy?.[0].with?.template).toBe('builtin://systemd-seamless-deploy')
    expect(pipeline.deploy?.[0].with?.vars).toMatchObject({ role: 'api_targets', app_name: 'api' })
    expect(pipeline.roles).toBeUndefined()
    expect(pipeline.variables?.app_name).toBe('api')
  })

  it('按阶段过滤模板并在所有阶段展示通用模板', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate, deployTemplate, packageTemplate],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')

    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    const buildOptions = wrapper.find('[data-test="block-0-template-select"]').findAll('option').map(option => option.text())
    expect(buildOptions.join('\n')).toContain('Go Build')
    expect(buildOptions.join('\n')).toContain('Archive Package')
    expect(buildOptions.join('\n')).not.toContain('Systemd Deploy')

    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    const deployOptions = wrapper.find('[data-test="block-1-template-select"]').findAll('option').map(option => option.text())
    expect(deployOptions.join('\n')).toContain('Systemd Deploy')
    expect(deployOptions.join('\n')).toContain('Archive Package')
    expect(deployOptions.join('\n')).not.toContain('Go Build')
  })

  it('导入模板后在当前阶段新增模板块并选中导入结果', async () => {
    const onImportTemplate = vi.fn().mockResolvedValue(importedBuildTemplate)
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate, importedBuildTemplate],
        initialMode: 'template',
        onImportTemplate,
      },
    })

    await wrapper.find('[data-test="import-template-build"]').trigger('click')

    expect(onImportTemplate).toHaveBeenCalled()
    expect(wrapper.findAll('.template-block')).toHaveLength(1)
    expect((wrapper.find('[data-test="block-0-template-select"]').element as HTMLSelectElement).value).toBe('user://imported-build@1.0.0')
    expect(wrapper.find('[data-test="pipeline-wizard-detail"]').text()).toContain('当前模板：Imported Build')
  })

  it('同阶段模板块可通过拖拽调整保存顺序', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [firstNoInputTemplate, secondNoInputTemplate],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://first-build@1.0.0')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-1-template-select"]').setValue('builtin://second-build@1.0.0')

    const blocks = wrapper.findAll('.template-block')
    await blocks[0].trigger('dragstart')
    await blocks[1].trigger('drop')
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')!.at(-1)![0] as Pipeline
    expect(pipeline.build?.map(step => step.with?.template)).toEqual([
      'builtin://second-build',
      'builtin://first-build',
    ])
  })

  it('阶段 tab 可切换当前模板输入详情', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate, deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://go-binary-build@1.0.0')
    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-1-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')

    await wrapper.find('[data-test="pipeline-phase-tab-build"]').trigger('click')
    expect(wrapper.find('[data-test="block-0-input-app_name"]').exists()).toBe(true)

    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    expect(wrapper.find('[data-test="block-1-input-role"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="block-1-role-targets"]').exists()).toBe(false)
  })

  it('target_role 使用变量输入且不渲染主机勾选网格', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }, { id: 'h2', name: 'Host 2' }],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')

    expect(wrapper.find('[data-test="block-0-runner-targets"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-role-targets"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-target-h1"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-input-role"]').exists()).toBe(true)
  })

  it('不渲染每模板 runner 主机勾选和展开按钮', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate],
        hosts: [
          { id: 'h1', name: 'Host 1' },
          { id: 'h2', name: 'Host 2' },
          { id: 'h3', name: 'Host 3' },
          { id: 'h4', name: 'Host 4' },
          { id: 'h5', name: 'Host 5' },
        ],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://go-binary-build@1.0.0')
    await wrapper.find('[data-test="block-0-input-app_name"]').setValue('api')

    expect(wrapper.find('[data-test="block-0-runner-h5"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-runner-more"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-runner-targets"]').exists()).toBe(false)
  })

  it('target_role 未选择机器时禁用保存', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')
    await wrapper.find('[data-test="block-0-input-app_name"]').setValue('api')

    expect(wrapper.find('[data-test="pipeline-save-template"]').attributes('disabled')).toBeDefined()
  })

  it('新建模板块不再通过每模板 runner 保存 include roles', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [buildTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://go-binary-build@1.0.0')
    await wrapper.find('[data-test="block-0-input-app_name"]').setValue('api')
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(pipeline.build?.[0].roles).toBeUndefined()
    expect(pipeline.roles?.build_0_runner).toBeUndefined()
  })

  it('已有 include pipeline 时回填模板、输入和运行组变量引用', () => {
    const pipeline: Pipeline = {
      roles: { deploy_0_targets: ['h1'] },
      deploy: [{
        name: 'Systemd Deploy',
        type: 'include',
        with: {
          template: 'builtin://systemd-seamless-deploy',
          version: '1.0.0',
          digest: 'sha256:deploy',
          vars: { app_name: 'api', role: 'deploy_0_targets' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: pipeline,
        templates: [deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })

    expect((wrapper.find('[data-test="block-0-template-select"]').element as HTMLSelectElement).value).toBe('builtin://systemd-seamless-deploy@1.0.0')
    expect((wrapper.find('[data-test="block-0-input-app_name"]').element as HTMLInputElement).value).toBe('api')
    expect((wrapper.find('[data-test="block-0-input-role"]').element as HTMLInputElement).value).toBe('deploy_0_targets')
    expect(wrapper.find('[data-test="block-0-target-h1"]').exists()).toBe(false)
  })

  it('已有 include roles 时不渲染每模板 runner 勾选且保存时移除旧 runner 数据', async () => {
    const pipeline: Pipeline = {
      roles: { build_0_runner: ['h1'] },
      build: [{
        name: 'Go Build',
        type: 'include',
        roles: ['build_0_runner'],
        with: {
          template: 'builtin://go-binary-build',
          version: '1.0.0',
          digest: 'sha256:build',
          vars: { app_name: 'api' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: pipeline,
        templates: [buildTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })

    expect(wrapper.find('[data-test="block-0-runner-h1"]').exists()).toBe(false)
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')
    const saved = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(saved.build?.[0].roles).toBeUndefined()
    expect(saved.roles?.build_0_runner).toBeUndefined()
  })

  it('已有项目级 roles 时只回填 target_role 变量名，不渲染主机勾选', async () => {
    const pipeline: Pipeline = {
      build: [{
        name: 'Go Build',
        type: 'include',
        with: {
          template: 'builtin://go-binary-build',
          version: '1.0.0',
          digest: 'sha256:build',
          vars: { app_name: 'api' },
        },
      }],
      deploy: [{
        name: 'Systemd Deploy',
        type: 'include',
        with: {
          template: 'builtin://systemd-seamless-deploy',
          version: '1.0.0',
          digest: 'sha256:deploy',
          vars: { role: 'deploy_1_targets', app_name: 'api' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: pipeline,
        pipelineRoles: {
          build_0_runner: { hosts: ['h1'] },
          deploy_1_targets: { hosts: ['h1'] },
        },
        templates: [buildTemplate, deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })

    expect(wrapper.find('[data-test="block-0-runner-h1"]').exists()).toBe(false)
    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    expect((wrapper.find('[data-test="block-1-input-role"]').element as HTMLInputElement).value).toBe('deploy_1_targets')
    expect(wrapper.find('[data-test="block-1-target-h1"]').exists()).toBe(false)
  })

  it('项目级 runner roles 后到时仍不渲染每模板 runner 勾选', async () => {
    const pipeline: Pipeline = {
      build: [{
        name: 'Go Build',
        type: 'include',
        with: {
          template: 'builtin://go-binary-build',
          version: '1.0.0',
          digest: 'sha256:build',
          vars: { app_name: 'api' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: pipeline,
        templates: [buildTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })
    expect(wrapper.find('[data-test="block-0-runner-h1"]').exists()).toBe(false)

    await wrapper.setProps({ pipelineRoles: { build_0_runner: { hosts: ['h1'] } } })

    expect(wrapper.find('[data-test="block-0-runner-h1"]').exists()).toBe(false)
  })

  it('roles 使用主机名时也不渲染每模板 runner checkbox', () => {
    const pipeline: Pipeline = {
      build: [{
        name: 'Go Build',
        type: 'include',
        with: {
          template: 'builtin://go-binary-build',
          version: '1.0.0',
          digest: 'sha256:build',
          vars: { app_name: 'api' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: pipeline,
        pipelineRoles: { build_0_runner: { hosts: ['Host 1'] } },
        templates: [buildTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })

    expect(wrapper.find('[data-test="block-0-runner-h1"]').exists()).toBe(false)
  })

  it('左栏模板卡片不展示运行机器和关键变量摘要', () => {
    const pipeline: Pipeline = {
      roles: { build_0_runner: ['h1'] },
      build: [{
        name: 'Go Build',
        type: 'include',
        roles: ['build_0_runner'],
        with: {
          template: 'builtin://go-binary-build',
          version: '1.0.0',
          digest: 'sha256:build',
          vars: { app_name: 'api' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: pipeline,
        templates: [buildTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })

    expect(wrapper.find('[data-test="block-0-inline-runner-targets"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-inline-key-vars"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-runner-targets"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="block-0-input-app_name"]').exists()).toBe(true)
  })

  it('展示预览结果和预览错误', () => {
    const preview: PipelinePreviewResponse = {
      run: {
        deployment_id: 'd1',
        status: 'pending',
        step_runs: [{ step_name: 'Compile', type: 'local_command', phase: 'build', status: 'pending', tasks: [] }],
      },
    }
    const wrapper = mount(PipelineTemplateWizard, {
      props: { modelValue: { deploy: [] }, templates: [buildTemplate], preview, previewError: '预览失败' },
    })

    expect(wrapper.text()).toContain('Compile')
    expect(wrapper.text()).toContain('预览失败')
  })

  it('保存 file_list 输入为结构化 files 数组', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: { modelValue: undefined, templates: [packageTemplate] },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://archive-package@1.0.0')
    await wrapper.find('[data-test="block-0-input-artifact"]').setValue('${artifacts}/api.tar.gz')
    expect(wrapper.find('[data-test="pipeline-save-template"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="block-0-add-file"]').text()).toBe('添加文件')

    await wrapper.find('[data-test="block-0-add-file"]').trigger('click')
    await wrapper.find('[data-test="block-0-file-from-0"]').setValue('${output}/api')
    await wrapper.find('[data-test="block-0-file-to-0"]').setValue('bin/api')
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(pipeline.build?.[0].with?.vars).toMatchObject({
      artifact: '${artifacts}/api.tar.gz',
      files: [{ from: '${output}/api', to: 'bin/api' }],
    })
  })

  it('右侧模板输入不再分组，按声明顺序单列展示', async () => {
    const template: PipelineTemplateSummary = {
      source: 'builtin',
      id: 'combined',
      category: 'build',
      name: 'Combined Build',
      version: '1.0.0',
      digest: 'sha256:combined',
      inputs: {
        frontend_dir: { label: '前端目录', type: 'path', required: true },
        build_command: { label: '构建命令', type: 'string', required: true },
        files: { label: '文件', type: 'file_list', required: true },
        skip_cache: { label: '跳过缓存', type: 'boolean', required: false, default: 'false' },
      },
    }
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [template],
        hosts: [{ id: 'h1', name: 'Host 1' }],
      },
    })

    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-build"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://combined@1.0.0')

    expect(wrapper.find('.detail-tabs').exists()).toBe(false)
    expect(wrapper.find('[data-test="input-group-path"]').exists()).toBe(false)
    expect(wrapper.find('.detail-count').text()).toContain('共 4 项')
    expect(wrapper.find('[data-test="block-0-input-frontend_dir"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="block-0-input-build_command"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="block-0-add-file"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="block-0-input-skip_cache"]').exists()).toBe(true)

    const detailText = wrapper.find('[data-test="pipeline-wizard-detail"]').text()
    expect(detailText.indexOf('前端目录')).toBeLessThan(detailText.indexOf('构建命令'))
    expect(detailText.indexOf('构建命令')).toBeLessThan(detailText.indexOf('文件'))
    expect(detailText.indexOf('文件')).toBeLessThan(detailText.indexOf('跳过缓存'))
  })

  it('已有 include pipeline 时回填 file_list 输入', () => {
    const pipeline: Pipeline = {
      build: [{
        name: 'Archive Package',
        type: 'include',
        with: {
          template: 'builtin://archive-package',
          version: '1.0.0',
          digest: 'sha256:package',
          vars: {
            artifact: '${artifacts}/api.tar.gz',
            files: [{ from: '${output}/api', to: 'bin/api' }],
          },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, {
      props: { modelValue: pipeline, templates: [packageTemplate] },
    })

    expect((wrapper.find('[data-test="block-0-input-artifact"]').element as HTMLInputElement).value).toBe('${artifacts}/api.tar.gz')
    expect((wrapper.find('[data-test="block-0-file-from-0"]').element as HTMLInputElement).value).toBe('${output}/api')
    expect((wrapper.find('[data-test="block-0-file-to-0"]').element as HTMLInputElement).value).toBe('bin/api')
  })

  it('boolean 模板输入用 checkbox 编辑并保存字符串值', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: { modelValue: undefined, templates: [booleanTemplate] },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')

    const checkbox = wrapper.find('[data-test="block-0-input-skip_http_check"]')
    expect((checkbox.element as HTMLInputElement).type).toBe('checkbox')
    await checkbox.setValue(true)
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(pipeline.deploy?.[0].with?.vars).toMatchObject({ skip_http_check: 'true' })
  })
})
