/**
 * SearchServiceColumns 组件测试。
 *
 * 职责：
 *   - 验证搜索上下文分栏只渲染可见命中服务
 *   - 验证单个可见服务会占满右侧可用区域
 *
 * 边界：
 *   - 不测试真实滚动像素，只验证组件生成的分栏结构和布局模板
 *   - 不请求真实 agent API，上下文数据直接写入 workspaceStore
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SearchServiceColumns from '../SearchServiceColumns.vue'
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

describe('SearchServiceColumns', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('隐藏服务不生成右侧分栏，单个可见服务占满可用区域', () => {
    const api = service('svc-api', 'api')
    const logger = service('svc-logger', 'logger')
    useAgentStore().projects = [project([api, logger])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 3, 'svc-logger': 2 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(1, 'svc-api', 'api visible')],
      'svc-logger': [log(2, 'svc-logger', 'logger hidden')],
    }
    workspace.hideService(tab.id, 'svc-logger')

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })

    const headers = wrapper.findAll('.column-header')
    expect(headers).toHaveLength(1)
    expect(headers[0].text()).toContain('api')
    expect(wrapper.text()).not.toContain('logger hidden')
    expect(wrapper.find('.columns-header').attributes('style')).toContain('1fr')
    expect(wrapper.find('.columns-header').attributes('style')).not.toContain('360px')
  })

  it('滚动到顶部时请求向上加载更多上下文', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 3 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(1, 'svc-api', 'api visible')],
    }
    const loadMore = vi.spyOn(workspace, 'loadMoreContext').mockResolvedValue(false)

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 0, writable: true, configurable: true })
    Object.defineProperty(columns, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(columns, 'scrollHeight', { value: 1200, configurable: true })

    await wrapper.find('.columns').trigger('scroll')

    expect(loadMore).toHaveBeenCalledWith(tab.id, 'before')
  })

  it('滚动到底部时请求向下加载更多上下文', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 3 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(1, 'svc-api', 'api visible')],
    }
    const loadMore = vi.spyOn(workspace, 'loadMoreContext').mockResolvedValue(false)

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 730, writable: true, configurable: true })
    Object.defineProperty(columns, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(columns, 'scrollHeight', { value: 1200, configurable: true })

    await wrapper.find('.columns').trigger('scroll')

    expect(loadMore).toHaveBeenCalledWith(tab.id, 'after')
  })

  it('上下文不足以滚动时可点击加载更早', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 3 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(1, 'svc-api', 'api visible')],
    }
    tab.hasMoreBeforeByService = { 'svc-api': true }
    const loadMore = vi.spyOn(workspace, 'loadMoreContext').mockResolvedValue(false)

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })

    await wrapper.find('.load-edge.before').trigger('click')

    expect(loadMore).toHaveBeenCalledWith(tab.id, 'before')
  })
})
