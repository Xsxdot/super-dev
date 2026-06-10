/**
 * BuildConfigBar 测试流水线级构建配置带。
 *
 * 职责：
 *   - 验证构建机器选择事件
 *   - 验证代码同步方式切换事件
 *
 * 边界：
 *   - 不保存 ProjectPipeline
 *   - 不调用真实 API
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import BuildConfigBar from '../BuildConfigBar.vue'
import { installTestI18n } from '@/test-utils/i18n'

const hosts = [
  { id: 'self-node', name: 'MacBook-Pro.local', is_self: true },
  { id: 'h1', name: 'ci-01' },
  { id: 'h2', name: 'ci-02' },
]

describe('BuildConfigBar', () => {
  it('uses the host list self node instead of adding a duplicate local option', () => {
    const wrapper = mount(BuildConfigBar, {
      props: { builderHostId: 'self-node', syncMode: 'transfer', hosts },
      global: { plugins: [installTestI18n()] },
    })

    const options = wrapper.findAll('[data-test="build-config-builder"] option')
    expect(options.map(option => option.text())).toEqual(['MacBook-Pro.local', 'ci-01', 'ci-02'])
  })

  it('hides sync mode when builder is the self node', () => {
    const wrapper = mount(BuildConfigBar, {
      props: { builderHostId: 'self-node', syncMode: 'transfer', hosts },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="build-config-sync"]').exists()).toBe(false)
  })

  it('emits builder host on select', async () => {
    const wrapper = mount(BuildConfigBar, {
      props: { builderHostId: 'h1', syncMode: 'transfer', hosts },
      global: { plugins: [installTestI18n()] },
    })
    const select = wrapper.get('[data-test="build-config-builder"]')
    await select.setValue('h2')
    expect(wrapper.emitted('update:builderHostId')?.[0]).toEqual(['h2'])
  })

  it('emits sync mode on toggle', async () => {
    const wrapper = mount(BuildConfigBar, {
      props: { builderHostId: 'h1', syncMode: 'transfer', hosts },
      global: { plugins: [installTestI18n()] },
    })
    expect(wrapper.find('[data-test="build-config-sync"]').exists()).toBe(true)
    await wrapper.get('[data-test="build-config-sync-remote_cmd"]').trigger('click')
    expect(wrapper.emitted('update:syncMode')?.[0]).toEqual(['remote_cmd'])
  })

  it('shows and emits target sync command in remote command mode', async () => {
    const wrapper = mount(BuildConfigBar, {
      props: { builderHostId: 'h1', syncMode: 'remote_cmd', syncCommand: '', hosts },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.get('[data-test="build-config-sync-command"]').setValue('git pull --ff-only')

    expect(wrapper.emitted('update:syncCommand')?.[0]).toEqual(['git pull --ff-only'])
  })
})
