/**
 * PanelToolbar i18n 测试日志面板工具栏文案。
 *
 * 职责：
 *   - 验证英文 locale 下过滤、规则和日志录制操作文案来自 i18n
 *
 * 边界：
 *   - 不测试过滤 store 的持久规则逻辑
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import PanelToolbar from '../PanelToolbar.vue'
import { installTestI18n } from '@/test-utils/i18n'

describe('PanelToolbar i18n', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('英文 locale 下渲染过滤工具栏文案', () => {
    const wrapper = mount(PanelToolbar, {
      props: { panelId: 'panel-1', projectId: 'proj-1', source: null },
      global: {
        plugins: [installTestI18n('en-US')],
        stubs: { RuleManagerModal: { template: '<div />' } },
      },
    })

    expect(wrapper.text()).toContain('Include')
    expect(wrapper.text()).toContain('Exclude')
    expect(wrapper.find('.chip-input').attributes('placeholder')).toBe('Filter keywords, press Enter to add')
    expect(wrapper.find('.rules-btn').attributes('title')).toBe('Manage filter rules')
    expect(wrapper.find('.bookmark-btn.start').attributes('title')).toBe('Start log recording')
  })
})
