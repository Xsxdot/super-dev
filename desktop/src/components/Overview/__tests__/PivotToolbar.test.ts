/**
 * PivotToolbar 测试运行状态页维度切换栏交互。
 *
 * 职责：
 *   - 验证一级/二级维度按钮的禁用与事件输出
 *   - 确认组件不直接依赖 settingsStore
 *
 * 边界：
 *   - 不验证 RuntimeStatusTab 分组渲染结果
 *   - 不测试 i18n 文案完整性
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PivotToolbar from '../PivotToolbar.vue'

describe('PivotToolbar', () => {
  it('当前 primary 在二级中置灰(disabled)', () => {
    const wrapper = mount(PivotToolbar, { props: { primary: 'env', secondary: 'service' } })
    const secondaryEnvBtn = wrapper.find('[data-test="secondary-env"]')
    expect(secondaryEnvBtn.attributes('disabled')).toBeDefined()
  })

  it('点击一级按钮触发 change 事件', async () => {
    const wrapper = mount(PivotToolbar, { props: { primary: 'env', secondary: 'service' } })
    await wrapper.find('[data-test="primary-node"]').trigger('click')
    const events = wrapper.emitted('change')
    expect(events).toBeTruthy()
    expect(events![0][0]).toBe('node')
  })

  it('点击二级按钮触发 change 事件,保持当前 primary', async () => {
    const wrapper = mount(PivotToolbar, { props: { primary: 'env', secondary: 'service' } })
    await wrapper.find('[data-test="secondary-node"]').trigger('click')
    const events = wrapper.emitted('change')!
    expect(events[0]).toEqual(['env', 'node'])
  })
})
