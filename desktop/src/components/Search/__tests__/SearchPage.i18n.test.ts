/**
 * SearchPage i18n 测试搜索页静态状态文案。
 *
 * 职责：
 *   - 验证英文 locale 下搜索输入、按钮和状态文案来自 i18n
 *
 * 边界：
 *   - 不执行真实搜索请求
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import SearchPage from '../SearchPage.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

describe('SearchPage i18n', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('英文 locale 下渲染搜索表单文案', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Project',
      root_path: '/tmp/project',
      services: [],
      env_selected_service_ids: {},
    }]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')

    const wrapper = mount(SearchPage, {
      props: { tabId: tab.id },
      global: {
        plugins: [installTestI18n('en-US')],
        stubs: { SearchBoard: { template: '<div />' } },
      },
    })

    expect(wrapper.find('[data-test="search-input"]').attributes('placeholder')).toBe(
      'Enter traceID, orderID, or error keyword...',
    )
    expect(wrapper.find('[data-test="search-submit"]').text()).toBe('Search')
  })
})
