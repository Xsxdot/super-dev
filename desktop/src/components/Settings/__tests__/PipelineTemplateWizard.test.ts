/**
 * PipelineTemplateWizard 测试模板化流水线组合编辑器。
 *
 * 职责：
 *   - 验证按阶段添加多个模板
 *   - 验证模板运行机器保存为 include roles
 *   - 验证 target_role 输入保存为 pipeline.roles
 *   - 验证已有 include pipeline 可回填
 *
 * 边界：
 *   - 不调用真实模板预览接口
 *   - 不解析模板 YAML
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
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
    expect(wrapper.find('[data-test="add-template-build"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-phase-tabs"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-wizard-detail"]').exists()).toBe(true)
  })

  it('按阶段保存多个模板和目标机器角色', async () => {
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

    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-1-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')
    await wrapper.find('[data-test="block-1-input-app_name"]').setValue('api')
    await wrapper.find('[data-test="block-1-target-h1"]').setValue(true)
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(pipeline.build?.[0].with?.template).toBe('builtin://go-binary-build')
    expect(pipeline.deploy?.[0].with?.template).toBe('builtin://systemd-seamless-deploy')
    expect(pipeline.deploy?.[0].with?.vars).toMatchObject({ role: 'deploy_1_targets', app_name: 'api' })
    expect(pipeline.roles?.deploy_1_targets).toEqual(['h1'])
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

    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    const deployOptions = wrapper.find('[data-test="block-1-template-select"]').findAll('option').map(option => option.text())
    expect(deployOptions.join('\n')).toContain('Systemd Deploy')
    expect(deployOptions.join('\n')).toContain('Archive Package')
    expect(deployOptions.join('\n')).not.toContain('Go Build')
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
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-1-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')

    await wrapper.find('[data-test="pipeline-phase-tab-build"]').trigger('click')
    expect(wrapper.find('[data-test="block-0-input-app_name"]').exists()).toBe(true)

    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    expect(wrapper.find('[data-test="block-1-role-targets"]').exists()).toBe(true)
  })

  it('机器选择使用紧凑网格展示', async () => {
    const wrapper = mount(PipelineTemplateWizard, {
      props: {
        modelValue: undefined,
        templates: [deployTemplate],
        hosts: [{ id: 'h1', name: 'Host 1' }, { id: 'h2', name: 'Host 2' }],
      },
    })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')

    expect(wrapper.find('[data-test="block-0-runner-targets"]').classes()).toContain('target-grid')
    expect(wrapper.find('[data-test="block-0-role-targets"]').classes()).toContain('target-grid')
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
    await wrapper.find('[data-test="add-template-deploy"]').trigger('click')
    await wrapper.find('[data-test="block-0-template-select"]').setValue('builtin://systemd-seamless-deploy@1.0.0')
    await wrapper.find('[data-test="block-0-input-app_name"]').setValue('api')

    expect(wrapper.find('[data-test="pipeline-save-template"]').attributes('disabled')).toBeDefined()
  })

  it('每个模板块可选择运行机器并保存 include roles', async () => {
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
    await wrapper.find('[data-test="block-0-runner-h1"]').setValue(true)
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const pipeline = wrapper.emitted('update:modelValue')![0][0] as Pipeline
    expect(pipeline.build?.[0].roles).toEqual(['build_0_runner'])
    expect(pipeline.roles?.build_0_runner).toEqual(['h1'])
  })

  it('已有 include pipeline 时回填模板、输入和目标机器', () => {
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
    expect((wrapper.find('[data-test="block-0-target-h1"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('已有 include roles 时回填模板运行机器', () => {
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

    expect((wrapper.find('[data-test="block-0-runner-h1"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('已有项目级 roles 时回填运行机器和 target_role 目标', async () => {
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

    expect((wrapper.find('[data-test="block-0-runner-h1"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.find('[data-test="pipeline-phase-tab-deploy"]').trigger('click')
    expect((wrapper.find('[data-test="block-1-target-h1"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('项目级 roles 后到时重新回填运行机器', async () => {
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
    expect((wrapper.find('[data-test="block-0-runner-h1"]').element as HTMLInputElement).checked).toBe(false)

    await wrapper.setProps({ pipelineRoles: { build_0_runner: { hosts: ['h1'] } } })

    expect((wrapper.find('[data-test="block-0-runner-h1"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('roles 使用主机名时也能回填对应 checkbox', () => {
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

    expect((wrapper.find('[data-test="block-0-runner-h1"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('选中模板卡片展示运行机器和关键变量摘要', () => {
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

    expect(wrapper.find('[data-test="block-0-inline-runner-targets"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="block-0-inline-key-vars"]').text()).toContain('api')
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

  it('按输入分组切换右侧模板输入', async () => {
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

    expect(wrapper.find('[data-test="input-group-path"]').text()).toContain('1')
    await wrapper.find('[data-test="input-group-file"]').trigger('click')
    expect(wrapper.find('[data-test="block-0-add-file"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="block-0-input-frontend_dir"]').exists()).toBe(false)

    await wrapper.find('[data-test="input-group-optional"]').trigger('click')
    expect(wrapper.find('[data-test="block-0-input-skip_cache"]').exists()).toBe(true)
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
