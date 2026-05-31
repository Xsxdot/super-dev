/**
 * PipelineTemplateWizard 测试模板化流水线配置入口。
 *
 * 职责：
 *   - 验证无 pipeline 时展示启用入口
 *   - 验证选择模板并填写输入后生成 include step
 *
 * 边界：
 *   - 不调用真实模板预览接口
 *   - 不解析模板 YAML
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import PipelineTemplateWizard from '@/components/Settings/PipelineTemplateWizard.vue'
import type { Pipeline, PipelineTemplateSummary } from '@/api/agent'

const template: PipelineTemplateSummary = {
  source: 'builtin',
  id: 'go-binary-build',
  name: 'Go Build',
  version: '1.0.0',
  digest: 'sha256:x',
  inputs: {
    app_name: { label: '应用名', type: 'string', required: true },
  },
}

describe('PipelineTemplateWizard', () => {
  it('无 pipeline 时展示配置入口', () => {
    const wrapper = mount(PipelineTemplateWizard, { props: { modelValue: undefined, templates: [template] } })
    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(true)
  })

  it('选择模板并填写 inputs 后 emit include step', async () => {
    const wrapper = mount(PipelineTemplateWizard, { props: { modelValue: undefined, templates: [template] } })
    await wrapper.find('[data-test="pipeline-enable"]').trigger('click')
    await wrapper.find('[data-test="template-select"]').setValue('builtin://go-binary-build@1.0.0')
    await wrapper.find('[data-test="template-input-app_name"]').setValue('api')
    await wrapper.find('[data-test="pipeline-save-template"]').trigger('click')

    const emitted = wrapper.emitted('update:modelValue')
    const pipeline = emitted![0][0] as any

    expect(pipeline.deploy[0].type).toBe('include')
    expect(pipeline.deploy[0].with.template).toBe('builtin://go-binary-build')
    expect(pipeline.deploy[0].with.version).toBe('1.0.0')
    expect(pipeline.deploy[0].with.digest).toBe('sha256:x')
    expect(pipeline.deploy[0].with.vars.app_name).toBe('api')
  })

  it('已有 include pipeline 时回填模板和 inputs', () => {
    const pipeline: Pipeline = {
      deploy: [{
        name: 'Go Build',
        type: 'include',
        with: {
          template: 'builtin://go-binary-build',
          version: '1.0.0',
          digest: 'sha256:x',
          vars: { app_name: 'api' },
        },
      }],
    }

    const wrapper = mount(PipelineTemplateWizard, { props: { modelValue: pipeline, templates: [template] } })

    expect((wrapper.find('[data-test="template-select"]').element as HTMLSelectElement).value).toBe('builtin://go-binary-build@1.0.0')
    expect((wrapper.find('[data-test="template-input-app_name"]').element as HTMLInputElement).value).toBe('api')
  })
})
