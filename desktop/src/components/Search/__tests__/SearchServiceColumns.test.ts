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
import { nextTick } from 'vue'
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

function log(
  id: number,
  serviceId: string,
  message: string,
  timestamp = '2026-05-20T22:41:32.000Z',
): LogEntry {
  return {
    id,
    service_id: serviceId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

function setRect(element: Element, top: number, bottom: number) {
  vi.spyOn(element, 'getBoundingClientRect').mockReturnValue({
    top,
    bottom,
    height: bottom - top,
    left: 0,
    right: 300,
    width: 300,
    x: 0,
    y: top,
    toJSON: () => ({}),
  } as DOMRect)
}

describe('SearchServiceColumns', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
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

  it('固定服务从共享滚动容器中剥离，避免跟随其他服务滚动', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 3, 'svc-worker': 2 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(1, 'svc-api', 'api pinned')],
      'svc-worker': [log(2, 'svc-worker', 'worker scrolling')],
    }
    workspace.pinService(tab.id, 'svc-api')

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })

    expect(wrapper.find('.pinned-columns').text()).toContain('api')
    expect(wrapper.find('.pinned-columns').text()).toContain('api pinned')
    expect(wrapper.find('.columns').text()).not.toContain('api pinned')
    expect(wrapper.findAll('.columns .column-header')).toHaveLength(1)
    expect(wrapper.find('.columns .column-header').text()).toContain('worker')
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

  it('右侧滚动到命中日志时同步左侧选中项', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 3 }
    tab.results = [
      log(20, 'svc-api', 'trace target 20', '2026-05-20T22:41:32.000Z'),
      log(30, 'svc-api', 'trace target 30', '2026-05-20T22:41:33.000Z'),
    ]
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [
        log(10, 'svc-api', 'ordinary context', '2026-05-20T22:41:31.000Z'),
        log(20, 'svc-api', 'trace target 20', '2026-05-20T22:41:32.000Z'),
        log(30, 'svc-api', 'trace target 30', '2026-05-20T22:41:33.000Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 300, writable: true, configurable: true })
    Object.defineProperty(columns, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(columns, 'scrollHeight', { value: 1200, configurable: true })
    setRect(columns, 0, 400)
    setRect(wrapper.find('[data-entry-id="10"]').element, 20, 44)
    setRect(wrapper.find('[data-entry-id="20"]').element, 180, 204)
    setRect(wrapper.find('[data-entry-id="30"]').element, 330, 354)

    await wrapper.find('.columns').trigger('scroll')

    expect(tab.selectedLogId).toBe(20)
  })

  it('右侧向下滚动时优先同步下方新进入视野的命中日志', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 3568, 'svc-server': 2 }
    tab.results = [
      log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      log(40, 'svc-server', 'server trace', '2026-05-21T02:00:55.683Z'),
    ]
    tab.selectedLogId = 20
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server trace', '2026-05-21T02:00:55.683Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 700, writable: true, configurable: true })
    Object.defineProperty(columns, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(columns, 'scrollHeight', { value: 1200, configurable: true })
    setRect(columns, 0, 400)
    setRect(wrapper.find('[data-entry-id="20"]').element, 180, 204)
    setRect(wrapper.find('[data-entry-id="40"]').element, 330, 354)

    await wrapper.find('.columns').trigger('scroll')

    expect(tab.selectedLogId).toBe(40)
  })

  it('外部选中命中项触发的程序滚动不会反向覆盖选中项', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 3568, 'svc-server': 2 }
    tab.results = [
      log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      log(40, 'svc-server', 'server trace', '2026-05-20T02:00:55.683Z'),
    ]
    tab.contextAnchorTime = '2026-05-20T02:00:55.683Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server trace', '2026-05-20T02:00:55.683Z'),
      ],
    }
    const scrollIntoView = vi.fn()
    window.HTMLElement.prototype.scrollIntoView = scrollIntoView

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 300, writable: true, configurable: true })
    Object.defineProperty(columns, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(columns, 'scrollHeight', { value: 1200, configurable: true })
    setRect(columns, 0, 400)
    setRect(wrapper.find('[data-entry-id="20"]').element, 180, 204)
    setRect(wrapper.find('[data-entry-id="40"]').element, 330, 354)

    workspace.searchTab(tab.id)!.selectedLogId = 40
    await nextTick()
    await nextTick()
    await wrapper.find('.columns').trigger('scroll')

    expect(tab.selectedLogId).toBe(40)
  })
})
