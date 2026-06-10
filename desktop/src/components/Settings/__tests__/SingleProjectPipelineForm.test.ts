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
  variables: { app_name: 'api' },
  environments: {
    test: { variables: { env: 'test' } },
    prod: { variables: { env: 'prod' } },
  },
  pipeline: {},
})

function mountForm(pipelineValue = pipeline()) {
  return mount(SingleProjectPipelineForm, {
    props: {
      pipeline: pipelineValue,
      services: [{ id: 'api', name: 'api' }, { id: 'web', name: 'web' }],
      hosts: [{ id: 'self-node', name: 'MacBook-Pro.local', is_self: true }, { id: 'h1', name: 'builder-01' }],
      templates: [],
      targetsByEnv: { test: ['web-test-01'], prod: ['web-prod-01'] },
    },
    global: { plugins: [installTestI18n()] },
  })
}

describe('SingleProjectPipelineForm', () => {
  it('edits one pipeline name and services', async () => {
    const wrapper = mountForm()

    expect(wrapper.find('[data-test="single-pipeline-form-topbar"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-station-base"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-phase-tabs"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-wizard-canvas"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-wizard-detail"]').exists()).toBe(true)

    await wrapper.find('[data-test="single-pipeline-name"]').setValue('Deploy Server Admin Prod')
    await wrapper.find('[data-test="single-pipeline-service-web"]').setValue(true)

    const emitted = wrapper.emitted('update:pipeline')!.at(-1)![0] as ProjectPipeline
    expect(emitted.name).toBe('Deploy Server Admin Prod')
    expect(emitted.services).toEqual(['api', 'web'])
  })

  it('renders build config bar and env matrix', () => {
    const wrapper = mountForm()

    expect(wrapper.find('[data-test="pipeline-config-panel-build"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-config-panel-variables"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-config-panel-deploy"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="build-config-bar"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-env-matrix"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="deploy-target-readonly"]').exists()).toBe(false)
  })

  it('opens only one configuration panel at a time', async () => {
    const wrapper = mountForm()

    await wrapper.get('[data-test="pipeline-config-toggle-build"]').trigger('click')
    expect(wrapper.find('[data-test="pipeline-config-body-build"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pipeline-config-body-variables"]').exists()).toBe(false)

    await wrapper.get('[data-test="pipeline-config-toggle-variables"]').trigger('click')
    expect(wrapper.find('[data-test="pipeline-config-body-build"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="pipeline-config-body-variables"]').exists()).toBe(true)
  })

  it('does not expose variable copy controls while variable panel is collapsed', () => {
    const wrapper = mountForm()

    expect(wrapper.get('[data-test="pipeline-config-panel-variables"]').text()).toContain('全局变量 1')
    expect(wrapper.find('[data-test="copy-var-app_name"]').exists()).toBe(false)
  })

  it('shows remote sync command only inside the opened build panel', async () => {
    const wrapper = mountForm({
      ...pipeline(),
      sync_mode: 'remote_cmd',
      sync_command: 'git fetch --all && git reset --hard origin/main',
      roles: { builder: { hosts: ['h1'] } },
    })

    expect(wrapper.find('[data-test="build-config-sync-command"]').exists()).toBe(false)

    await wrapper.get('[data-test="pipeline-config-toggle-build"]').trigger('click')

    expect(wrapper.get('[data-test="build-config-builder"]').element).toHaveProperty('value', 'h1')
    expect(wrapper.find('[data-test="build-config-sync"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="build-config-sync-command"]').element).toHaveProperty(
      'value',
      'git fetch --all && git reset --hard origin/main',
    )
  })

  it('updates sync_mode through build config bar', async () => {
    const wrapper = mountForm({
      ...pipeline(),
      roles: { builder: { hosts: ['h1'] } },
    })

    await wrapper.get('[data-test="pipeline-config-toggle-build"]').trigger('click')
    await wrapper.get('[data-test="build-config-sync-remote_cmd"]').trigger('click')
    const emitted = wrapper.emitted('update:pipeline')
    expect(emitted?.at(-1)?.[0]).toMatchObject({ sync_mode: 'remote_cmd' })
  })

  it('normalizes sync mode to transfer when selecting the self node', async () => {
    const wrapper = mountForm({
      ...pipeline(),
      sync_mode: 'remote_cmd',
      roles: { builder: { hosts: ['h1'] } },
    })

    await wrapper.get('[data-test="pipeline-config-toggle-build"]').trigger('click')
    await wrapper.get('[data-test="build-config-builder"]').setValue('self-node')
    const emitted = wrapper.emitted('update:pipeline')
    expect(emitted?.at(-1)?.[0]).toMatchObject({
      sync_mode: 'transfer',
      roles: { builder: { hosts: ['self-node'] } },
    })
  })
})
