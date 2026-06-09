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
  { id: 'h1', name: 'ci-01' },
  { id: 'h2', name: 'ci-02' },
]

describe('BuildConfigBar', () => {
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
    await wrapper.get('[data-test="build-config-sync-remote_cmd"]').trigger('click')
    expect(wrapper.emitted('update:syncMode')?.[0]).toEqual(['remote_cmd'])
  })
})
