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
  reservedNames: ['artifacts', 'workspace', 'version', 'env'],
  availableEnvironments: ['test', 'prod', 'staging'],
  hosts: [
    { id: 'h1', name: 'Host 1' },
    { id: 'h2', name: 'Host 2' },
    { id: 'h3', name: 'Host 3' },
    { id: 'h4', name: 'Host 4' },
  ],
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

  it('shows delete actions for custom variables and run groups', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { local01_targets: { environments: { test: ['h1'], prod: ['h2'] } } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    expect(wrapper.findAll('th').map(th => th.text())).toContain('操作')
    expect(wrapper.find('[data-test="delete-var-app_name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="delete-role-local01_targets"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="delete-var-env"]').exists()).toBe(false)
  })

  it('shows single-column matrix when single environment', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: { ...baseProps(), environments: { test: { variables: { env: 'test' } } } },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    // 单环境也要能编辑：矩阵区存在，且退化为单列（只有 test 列）
    expect(wrapper.find('[data-test="env-matrix"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="env-col-test"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="env-col-prod"]').exists()).toBe(false)
  })

  it('hides matrix only when no environment selected', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: { ...baseProps(), environments: {} },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    expect(wrapper.find('[data-test="env-matrix"]').exists()).toBe(false)
  })

  it('adds a new variable with an optional default value', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    await wrapper.get('[data-test="global-var-name-input"]').setValue('region')
    await wrapper.get('[data-test="global-var-value-input"]').setValue('cn')
    await wrapper.get('[data-test="global-var-add"]').trigger('click')
    expect(wrapper.emitted('update:variables')?.[0][0]).toMatchObject({
      app_name: 'myapp',
      region: 'cn',
    })
  })

  it('forbids unselecting the last remaining environment', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: { ...baseProps(), environments: { test: { variables: { env: 'test' } } } },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    // 取消最后一个环境应被阻止：setValue(false) 使 change 走取消分支，但不应 emit
    await wrapper.get('[data-test="env-select-test"]').setValue(false)
    expect(wrapper.emitted('update:environments')).toBeFalsy()
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

  it('shows copy icons beside copyable variable names', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { local01_targets: { environments: { test: ['h1'], prod: ['h2'] } } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    expect(wrapper.find('[data-test="copy-var-app_name-icon"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="copy-var-local01_targets-icon"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="copy-var-artifacts-icon"]').exists()).toBe(true)
  })

  it('shows copied feedback after variable name click', async () => {
    const writeText = vi.fn()
    Object.assign(navigator, { clipboard: { writeText } })
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    await wrapper.get('[data-test="copy-var-app_name"]').trigger('click')

    expect(wrapper.get('[data-test="copy-var-app_name-feedback"]').text()).toContain('已复制')
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

  it('adds global variables on demand', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    await wrapper.get('[data-test="global-var-name-input"]').setValue('artifact')
    await wrapper.get('[data-test="global-var-value-input"]').setValue('${output}/app.tar.gz')
    await wrapper.get('[data-test="global-var-add"]').trigger('click')

    expect(wrapper.emitted('update:variables')?.[0][0]).toMatchObject({
      app_name: 'myapp',
      artifact: '${output}/app.tar.gz',
    })
  })

  it('renders add controls in a standalone toolbar below the variable table', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    expect(wrapper.find('[data-test="variable-add-toolbar"]').exists()).toBe(true)
    expect(wrapper.find('tr.emr-add-line').exists()).toBe(false)
    expect(wrapper.get('[data-test="global-var-name-input"]').attributes('placeholder')).toBe('新变量名')
    expect(wrapper.get('[data-test="global-var-value-input"]').attributes('placeholder')).toBe('默认值（可选）')
    expect(wrapper.get('[data-test="run-group-name-input"]').attributes('placeholder')).toBe('新运行组名')
    expect(wrapper.find('select[data-test="run-group-host-input"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="run-group-host-trigger"]').exists()).toBe(true)
  })

  it('deletes a custom variable from global and environment values', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        variables: { app_name: 'myapp', artifact: 'app.tar.gz' },
        environments: {
          test: { variables: { env: 'test', artifact: 'test.tar.gz' } },
          prod: { variables: { env: 'prod', artifact: 'prod.tar.gz' } },
        },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    await wrapper.get('[data-test="delete-var-artifact"]').trigger('click')

    expect(wrapper.emitted('update:variables')?.[0][0]).toEqual({ app_name: 'myapp' })
    expect(wrapper.emitted('update:environments')?.[0][0]).toEqual({
      test: { variables: { env: 'test' } },
      prod: { variables: { env: 'prod' } },
    })
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

  it('renders run groups as special rows inside the environment matrix', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { local01_targets: { environments: { test: ['h1', 'h2'], prod: ['h3', 'h4'] } } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    const row = wrapper.get('[data-test="env-var-row-local01_targets"]')
    expect(row.text()).toContain('local01_targets')
    expect(row.text()).toContain('运行组')
    expect(row.get('[data-test="role-hosts-test-local01_targets"]').text()).toContain('Host 1')
    expect(row.get('[data-test="role-hosts-prod-local01_targets"]').text()).toContain('Host 3')
    expect(wrapper.find('[data-test="run-groups"]').exists()).toBe(false)
  })

  it('opens run group host dropdowns inside the variable table', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { local01_targets: { environments: { test: ['h1'], prod: ['h2'] } } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    expect(wrapper.find('[data-test="role-host-menu-test-local01_targets"]').exists()).toBe(false)
    await wrapper.get('[data-test="role-host-trigger-test-local01_targets"]').trigger('click')

    const menu = wrapper.get('[data-test="role-host-menu-test-local01_targets"]')
    expect(menu.text()).toContain('Host 1')
    expect(menu.text()).toContain('Host 2')
    expect((wrapper.get('[data-test="role-host-test-local01_targets-h1"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('shows copied feedback after run group variable click', async () => {
    const writeText = vi.fn()
    Object.assign(navigator, { clipboard: { writeText } })
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { compute: { hosts: ['h1'] } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    await wrapper.get('[data-test="copy-var-compute"]').trigger('click')

    expect(writeText).toHaveBeenCalledWith('${compute}')
    expect(wrapper.get('[data-test="copy-var-compute-feedback"]').text()).toContain('已复制')
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
    expect(wrapper.find('[data-test="env-var-row-builder"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="env-var-row-build_0_runner"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="env-var-row-compute"]').exists()).toBe(true)
  })

  it('edits run group host lists per environment', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { compute: { environments: { test: ['h1'] } } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    await wrapper.get('[data-test="role-host-trigger-test-compute"]').trigger('click')
    await wrapper.get('[data-test="role-host-test-compute-h2"]').setValue(true)
    expect(wrapper.emitted('update:roles')?.[0][0]).toMatchObject({
      compute: { environments: { test: ['h1', 'h2'] } },
    })
  })

  it('checks run group hosts saved by host name', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: { compute: { hosts: ['Host 1'] } },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)
    await wrapper.get('[data-test="role-host-trigger-test-compute"]').trigger('click')

    const checkbox = wrapper.get('[data-test="role-host-test-compute-h1"]').element as HTMLInputElement
    expect(checkbox.checked).toBe(true)

    await wrapper.get('[data-test="role-host-test-compute-h1"]').setValue(false)
    expect(wrapper.emitted('update:roles')?.[0][0]).toMatchObject({
      compute: { environments: { test: [] } },
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
      nginx_upstream: { environments: { test: [], prod: [] } },
    })
  })

  it('adds run group variables with multiple selected hosts', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: baseProps(),
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    await wrapper.get('[data-test="run-group-name-input"]').setValue('api_targets')
    await wrapper.get('[data-test="run-group-host-trigger"]').trigger('click')
    expect(wrapper.get('[data-test="run-group-host-menu"]').text()).toContain('Host 1')
    await wrapper.get('[data-test="run-group-host-option-h1"]').setValue(true)
    await wrapper.get('[data-test="run-group-host-option-h2"]').setValue(true)
    await wrapper.get('[data-test="run-group-add"]').trigger('click')

    expect(wrapper.get('[data-test="run-group-host-trigger"]').text()).toContain('选择主机')
    expect(wrapper.emitted('update:roles')?.[0][0]).toMatchObject({
      api_targets: { environments: { test: ['h1', 'h2'], prod: ['h1', 'h2'] } },
    })
  })

  it('deletes run group variables on demand', async () => {
    const wrapper = mount(PipelineEnvMatrix, {
      props: {
        ...baseProps(),
        roles: {
          builder: { hosts: ['h1'] },
          local01_targets: { environments: { test: ['h1'], prod: ['h2'] } },
        },
      },
      global: { plugins: [installTestI18n()] },
    })
    await openMatrix(wrapper)

    await wrapper.get('[data-test="delete-role-local01_targets"]').trigger('click')

    expect(wrapper.emitted('update:roles')?.[0][0]).toEqual({
      builder: { hosts: ['h1'] },
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
