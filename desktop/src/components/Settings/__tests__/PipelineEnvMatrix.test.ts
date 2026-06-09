/**
 * PipelineEnvMatrix 测试流水线变量矩阵。
 *
 * 职责：
 *   - 验证全局变量和多环境变量矩阵渲染
 *   - 验证变量名点击复制占位符
 *   - 验证变量更新事件
 *
 * 边界：
 *   - 不保存 ProjectPipeline
 *   - 不解析模板变量
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PipelineEnvMatrix from '../PipelineEnvMatrix.vue'
import { installTestI18n } from '@/test-utils/i18n'

const baseProps = () => ({
  variables: { app_name: 'myapp' },
  environments: {
    test: { variables: { env: 'test', db_url: 'test-db' } },
    prod: { variables: { env: 'prod', db_url: 'prod-db' } },
  },
  reservedNames: ['artifact', 'workspace', 'version', 'env'],
})

describe('PipelineEnvMatrix', () => {
  it('renders one column per environment', () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.get('[data-test="env-col-test"]').text()).toContain('test')
    expect(wrapper.get('[data-test="env-col-prod"]').text()).toContain('prod')
  })

  it('hides matrix when single environment', () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: { ...baseProps(), environments: { test: { variables: { env: 'test' } } } },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.find('[data-test="env-matrix"]').exists()).toBe(false)
  })

  it('copies ${var} to clipboard on variable name click', async () => {
    const writeText = vi.fn()
    Object.assign(navigator, { clipboard: { writeText } })
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await wrapper.get('[data-test="copy-var-app_name"]').trigger('click')
    expect(writeText).toHaveBeenCalledWith('${app_name}')
  })

  it('emits updated global variable', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    const input = wrapper.get('[data-test="global-var-value-app_name"]')
    await input.setValue('renamed')
    expect(wrapper.emitted('update:variables')?.[0][0]).toMatchObject({ app_name: 'renamed' })
  })
})
