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
  availableEnvironments: ['test', 'prod', 'staging'],
  hosts: [{ id: 'h1', name: 'Host 1' }, { id: 'h2', name: 'Host 2' }],
})

async function openMatrix(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('[data-test="env-matrix-toggle"]').trigger('click')
}

describe('PipelineEnvMatrix', () => {
  it('collapses variable editor into summary by default', () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.get('[data-test="env-matrix-summary"]').text()).toContain('app_name')
    expect(wrapper.find('[data-test="env-matrix"]').exists()).toBe(false)
  })

  it('renders one column per environment', () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    return openMatrix(wrapper).then(() => {
    expect(wrapper.get('[data-test="env-col-test"]').text()).toContain('test')
    expect(wrapper.get('[data-test="env-col-prod"]').text()).toContain('prod')
    })
  })

  it('hides matrix when single environment', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: { ...baseProps(), environments: { test: { variables: { env: 'test' } } } },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    expect(wrapper.find('[data-test="env-matrix"]').exists()).toBe(false)
  })

  it('copies ${var} to clipboard on variable name click', async () => {
    const writeText = vi.fn()
    Object.assign(navigator, { clipboard: { writeText } })
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    await wrapper.get('[data-test="copy-var-app_name"]').trigger('click')
    expect(writeText).toHaveBeenCalledWith('${app_name}')
  })

  it('emits updated global variable', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    const input = wrapper.get('[data-test="global-var-value-app_name"]')
    await input.setValue('renamed')
    expect(wrapper.emitted('update:variables')?.[0][0]).toMatchObject({ app_name: 'renamed' })
  })

  it('selects project environments into the pipeline subset', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: { ...baseProps(), environments: { test: { variables: { env: 'test' } } } },
      global: { plugins: [installTestI18n()] },
    })
    await wrapper.get('[data-test="env-select-staging"]').setValue(true)
    expect(wrapper.emitted('update:environments')?.[0][0]).toMatchObject({
      test: { variables: { env: 'test' } },
      staging: { variables: { env: 'staging' } },
    })
  })

  it('renders run group rows when roles provided', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { compute: { from_service: 'api' } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    expect(wrapper.get('[data-test="run-group-compute"]').text()).toContain('compute')
  })

  it('hides builder and legacy runner roles from run groups', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: {
          builder: { hosts: ['h1'] },
          build_0_runner: { hosts: ['h1'] },
          compute: { hosts: ['h2'] },
        },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    expect(wrapper.find('[data-test="run-group-builder"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="run-group-build_0_runner"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="run-group-compute"]').exists()).toBe(true)
  })

  it('edits run group host lists', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { compute: { hosts: ['h1'] } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    await wrapper.get('[data-test="run-group-compute-host-h2"]').setValue(true)
    expect(wrapper.emitted('update:roles')?.[0][0]).toMatchObject({
      compute: { hosts: ['h1', 'h2'] },
    })
  })

  it('adds run group variables on demand', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    await wrapper.get('[data-test="run-group-name-input"]').setValue('nginx_upstream')
    await wrapper.get('[data-test="run-group-add"]').trigger('click')
    expect(wrapper.emitted('update:roles')?.[0][0]).toMatchObject({
      nginx_upstream: { hosts: [] },
    })
  })

  it('does not render run group rows when no roles', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    expect(wrapper.find('[data-test="run-group-compute"]').exists()).toBe(false)
  })
})
