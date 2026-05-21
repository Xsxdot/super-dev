/**
 * RuleManagerModal 测试项目级过滤规则管理交互。
 *
 * 职责：
 *   - 验证已有规则渲染
 *   - 验证新增规则提交到 filterStore
 *   - 验证当前面板 chip 可保存为项目规则
 *
 * 边界：
 *   - 不测试后端持久化细节，store 测试覆盖 API 调用
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuleManagerModal from '../RuleManagerModal.vue'
import { useFilterStore } from '@/stores/filter'

describe('RuleManagerModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('渲染当前项目规则', () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = [{
      id: 'rule-1',
      name: 'Errors',
      type: 'include',
      keywords: ['error'],
      logic: 'or',
      enabled: true,
    }]

    const wrapper = mount(RuleManagerModal, {
      props: { projectId: 'proj-1', panelId: 'panel-1' },
    })

    expect(wrapper.text()).toContain('Errors')
    expect(wrapper.text()).toContain('error')
  })

  it('提交新增规则', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    vi.spyOn(store, 'createRule').mockResolvedValue(undefined)
    const wrapper = mount(RuleManagerModal, {
      props: { projectId: 'proj-1', panelId: 'panel-1' },
    })

    await wrapper.find('[data-test="new-rule"]').trigger('click')
    await wrapper.find('[data-test="rule-name"]').setValue('Errors')

    const keywordsInput = wrapper.find('[data-test="rule-keywords"]')
    await keywordsInput.setValue('error')
    await keywordsInput.trigger('keydown', { key: 'Enter' })
    await keywordsInput.setValue('timeout')
    await keywordsInput.trigger('keydown', { key: 'Enter' })

    await wrapper.find('[data-test="save-rule"]').trigger('click')

    expect(store.createRule).toHaveBeenCalledWith('proj-1', {
      name: 'Errors',
      type: 'include',
      keywords: ['error', 'timeout'],
      logic: 'or',
      enabled: true,
    })
  })

  it('从当前面板 chip 保存规则', async () => {
    const store = useFilterStore()
    store.projectRules['proj-1'] = []
    store.addChip('panel-1', 'error', 'include')
    vi.spyOn(store, 'savePanelChipsAsRule').mockResolvedValue(undefined)
    const wrapper = mount(RuleManagerModal, {
      props: { projectId: 'proj-1', panelId: 'panel-1' },
    })

    await wrapper.find('[data-test="save-current-filter"]').trigger('click')
    await wrapper.find('[data-test="rule-name"]').setValue('Current filter')
    await wrapper.find('[data-test="save-rule"]').trigger('click')

    expect(store.savePanelChipsAsRule).toHaveBeenCalledWith('proj-1', 'panel-1', {
      name: 'Current filter',
      type: 'include',
      keywords: ['error'],
      logic: 'or',
      enabled: true,
    })
  })
})
