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
  name: 'Systemd Deploy',
  version: '1.0.0',
  digest: 'sha256:deploy',
  inputs: {
    role: { label: '目标机器', type: 'target_role', required: true, description: '部署目标机器' },
    app_name: { label: '应用名', type: 'string', required: true, description: '应用名' },
  },
}

describe('PipelineTemplateWizard', () => {
  it('无 pipeline 时展示配置入口', () => {
    const wrapper = mount(PipelineTemplateWizard, { props: { modelValue: undefined, templates: [buildTemplate] } })
    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(true)
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
})
