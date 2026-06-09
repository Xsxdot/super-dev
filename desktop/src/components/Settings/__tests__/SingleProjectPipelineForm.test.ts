/**
 * SingleProjectPipelineForm 测试单流水线编辑表单。
 *
 * 职责：
 *   - 验证只编辑一条 ProjectPipeline
 *   - 验证服务选择和名称变更通过 update:pipeline 返回
 *
 * 边界：
 *   - 不保存项目配置
 *   - 不调用真实 API
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SingleProjectPipelineForm from '@/components/Settings/SingleProjectPipelineForm.vue'
import type { ProjectPipeline } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'

const pipeline = (): ProjectPipeline => ({
  id: 'deploy-prod',
  name: 'Deploy Prod',
  services: ['api'],
  artifact_kind: 'file',
  pipeline: {},
})

describe('SingleProjectPipelineForm', () => {
  it('edits one pipeline name and services', async () => {
    const wrapper = mount(SingleProjectPipelineForm, {
      props: {
        pipeline: pipeline(),
        services: [{ id: 'api', name: 'api' }, { id: 'web', name: 'web' }],
        hosts: [],
        templates: [],
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="single-pipeline-form-topbar"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-phase-tabs"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-wizard-canvas"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-wizard-detail"]').exists()).toBe(true)

    await wrapper.find('[data-test="single-pipeline-name"]').setValue('Deploy Server Admin Prod')
    await wrapper.find('[data-test="single-pipeline-service-web"]').setValue(true)

    const emitted = wrapper.emitted('update:pipeline')!.at(-1)![0] as ProjectPipeline
    expect(emitted.name).toBe('Deploy Server Admin Prod')
    expect(emitted.services).toEqual(['api', 'web'])
  })
})
