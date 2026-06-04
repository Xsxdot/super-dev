/**
 * PanelToolbar i18n 测试日志面板工具栏文案。
 *
 * 职责：
 *   - 验证英文 locale 下过滤、规则和书签操作文案来自 i18n
 *
 * 边界：
 *   - 不测试过滤 store 的持久规则逻辑
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import PanelToolbar from '../PanelToolbar.vue'
import { installTestI18n } from '@/test-utils/i18n'
import { useFilterStore } from '@/stores/filter'

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
    expect(wrapper.find('.bookmark-btn.start').attributes('title')).toBe('Start bookmark recording')
  })

  it('紧凑模式保留过滤、规则和书签核心操作', () => {
    const filterStore = useFilterStore()
    filterStore.addChip('panel-1', 'timeout', 'include')

    const wrapper = mount(PanelToolbar, {
      props: { panelId: 'panel-1', projectId: 'proj-1', source: null, compact: true },
      global: {
        plugins: [installTestI18n('en-US')],
        stubs: { RuleManagerModal: { template: '<div />' } },
      },
    })

    expect(wrapper.find('[data-test="panel-toolbar"]').classes()).toContain('compact')
    expect(wrapper.find('.chip-input').exists()).toBe(true)
    expect(wrapper.find('.rules-btn').exists()).toBe(true)
    expect(wrapper.find('.save-rule-btn').attributes('title')).toBe('Save as Rule')
    expect(wrapper.find('.bookmark-btn.start').exists()).toBe(true)
  })
})
