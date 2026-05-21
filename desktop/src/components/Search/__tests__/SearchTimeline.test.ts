/**
 * SearchTimeline 组件测试。
 *
 * 职责：
 *   - 验证搜索命中时间线的选择与定位行为
 *
 * 边界：
 *   - 不测试真实 API 请求
 *   - 不测试滚动像素，只验证选中项变化时会触发行定位
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import SearchTimeline from '../SearchTimeline.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import type { LogEntry, Project, Service } from '@/api/agent'

function service(id: string, name: string): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

function log(id: number, serviceId: string, message: string): LogEntry {
  return {
    id,
    service_id: serviceId,
    run_id: 'run-1',
    timestamp: '2026-05-20T22:41:32.000Z',
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('SearchTimeline', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    setActivePinia(createPinia())
  })

  it('选中项变化时滚动到对应命中日志', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.results = [
      log(20, 'svc-api', 'trace target 20'),
      log(30, 'svc-api', 'trace target 30'),
    ]
    const scrollIntoView = vi.fn()
    window.HTMLElement.prototype.scrollIntoView = scrollIntoView

    const wrapper = mount(SearchTimeline, {
      props: { tabId: tab.id },
    })

    workspace.searchTab(tab.id)!.selectedLogId = 30
    await nextTick()
    await nextTick()

    expect(wrapper.find('.timeline-row.selected').text()).toContain('trace target 30')
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' })
  })

  it('滚动到命中列表底部时加载更多搜索结果', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.results = [log(20, 'svc-api', 'trace target 20')]
    tab.serviceCounts = { 'svc-api': 2 }
    const loadMore = vi.spyOn(workspace, 'loadMoreSearchResults').mockResolvedValue(false)

    const wrapper = mount(SearchTimeline, {
      props: { tabId: tab.id },
    })
    const timeline = wrapper.find('.timeline').element as HTMLElement
    Object.defineProperty(timeline, 'scrollTop', { value: 730, writable: true, configurable: true })
    Object.defineProperty(timeline, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(timeline, 'scrollHeight', { value: 1200, configurable: true })

    await wrapper.find('.timeline').trigger('scroll')

    expect(loadMore).toHaveBeenCalledWith(tab.id)
  })
})
