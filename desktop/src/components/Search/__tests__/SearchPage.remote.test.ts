/**
 * SearchPage.remote 测试跨节点远程搜索模式。
 *
 * 职责：
 *   - 验证远程模式提交查询调用 api.remoteSearch
 *   - 验证失败 Host 提示和结果渲染
 *
 * 边界：
 *   - 不测试本地项目搜索分支
 */
import { describe, it, expect, beforeEach, vi, type Mock } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import SearchPage from '@/components/Search/SearchPage.vue'
import { api } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      remoteSearch: vi.fn().mockResolvedValue({
        entries: [
          {
            id: 1,
            service_id: 's',
            run_id: 'r',
            timestamp: '2026-05-21T12:00:00Z',
            level: 'INFO',
            message: 'hit-A',
            stream: 'stdout',
            host_id: 'h1',
          },
        ],
        total_by_host: { h1: 1 },
        hosts_failed: ['h2'],
        next_cursor: 'cur-2',
        has_more: true,
      }),
      searchLogs: vi.fn(),
    },
  }
})

describe('SearchPage 远程模式', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('提交查询时调 api.remoteSearch', async () => {
    const wrapper = mount(SearchPage, {
      props: { logSourceId: 'ls1', groupKey: 'prod' },
    })

    await wrapper.find('[data-test="search-input"]').setValue('trace-8f21')
    await wrapper.find('form').trigger('submit.prevent')

    expect(api.remoteSearch).toHaveBeenCalled()
    expect((api.remoteSearch as Mock).mock.calls[0][0]).toMatchObject({
      log_source_id: 'ls1',
      group: 'prod',
      query: 'trace-8f21',
    })
    expect(api.searchLogs).not.toHaveBeenCalled()
  })

  it('展示失败 host 提示和结果 host 前缀', async () => {
    const wrapper = mount(SearchPage, {
      props: { logSourceId: 'ls1', groupKey: 'prod' },
    })

    await wrapper.find('[data-test="search-input"]').setValue('trace-8f21')
    await wrapper.find('form').trigger('submit.prevent')

    expect(wrapper.text()).toContain('以下节点超时或失败')
    expect(wrapper.text()).toContain('h2')
    expect(wrapper.text()).toContain('[h1]')
    expect(wrapper.text()).toContain('hit-A')
  })
})
