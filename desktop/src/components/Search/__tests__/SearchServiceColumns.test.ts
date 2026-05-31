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
    env_selected_service_ids: {},
  }
}

function log(
  id: number,
  deploymentId: string,
  message: string,
  timestamp = '2026-05-20T22:41:32.000Z',
): LogEntry {
  return {
    id,
    deployment_id: deploymentId,
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
    const visibleScrollText = wrapper
      .findAll('.columns .scroll-cell-layer:not(.height-mirror)')
      .map(layer => layer.text())
      .join('')
    expect(visibleScrollText).not.toContain('api pinned')
    expect(wrapper.findAll('.columns .column-header')).toHaveLength(1)
    expect(wrapper.find('.columns .column-header').text()).toContain('worker')
  })

  it('固定栏保留固定前的完整时间栅格，避免内容因行数收缩而漂移', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 3568, 'svc-server': 2 }
    tab.contextAnchorTime = '2026-05-20T02:00:55.683Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server trace', '2026-05-20T02:00:55.683Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')

    const pinnedRows = wrapper.findAll('.pinned-row')
    expect(pinnedRows).toHaveLength(2)
    expect(pinnedRows[0].find('.bucket-cell').classes()).toContain('blank')
    expect(pinnedRows[1].text()).toContain('logger trace')
  })

  it('固定栏为其它服务保留隐藏行高镜像，避免同一时间行按单列高度重新收缩', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 1, 'svc-server': 1 }
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server line 1\nserver line 2\nserver line 3', '2026-05-20T14:41:43.501Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')

    const cells = wrapper.find('.pinned-row').findAll('.bucket-cell')
    expect(cells).toHaveLength(1)
    expect(cells[0].attributes('data-service-id')).toBe('svc-logger')
    const layers = cells[0].findAll('.pinned-cell-layer')
    expect(layers).toHaveLength(2)
    expect(layers[0].attributes('data-service-id')).toBe('svc-logger')
    expect(layers[0].classes()).not.toContain('height-mirror')
    expect(layers[1].attributes('data-service-id')).toBe('svc-server')
    expect(layers[1].classes()).toContain('height-mirror')
    expect(layers[1].attributes('aria-hidden')).toBe('true')
  })

  it('固定栏使用自身 scrollTop 保持位置，不用 transform 把内容平移到视口外', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 3, 'svc-server': 2 }
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server trace', '2026-05-20T14:41:44.401Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 360, writable: true, configurable: true })

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')
    await nextTick()

    expect((wrapper.find('.pinned-body').element as HTMLElement).scrollTop).toBe(360)
    expect(wrapper.find('.pinned-grid').attributes('style') ?? '').not.toContain('translateY')
  })

  it('固定服务后右侧滚动栏仍保留完整时间轴，避免剩余服务跳到另一段时间', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 2, 'svc-server': 2 }
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger only row', '2026-05-20T14:41:43.401Z'),
        log(21, 'svc-logger', 'logger shared row', '2026-05-20T14:41:44.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server shared row', '2026-05-20T14:41:44.501Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')

    const rows = wrapper.findAll('.columns .bucket-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('14:41:43')
    expect(rows[0].find('.bucket-cell').classes()).toContain('blank')
    expect(rows[0].find('.scroll-cell-layer.height-mirror').attributes('data-service-id')).toBe('svc-logger')
    expect(rows[1].text()).toContain('server shared row')
  })

  it('固定后沿用固定前的列宽，避免单侧长日志因重新换行造成行高偏移', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 2, 'svc-server': 2 }
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger only row with a long payload that wraps', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server only row with a long payload that wraps', '2026-05-20T14:41:44.501Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const headers = wrapper.findAll('.columns .column-header')
    vi.spyOn(headers[0].element, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      bottom: 32,
      height: 32,
      left: 0,
      right: 420,
      width: 420,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)
    vi.spyOn(headers[1].element, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      bottom: 32,
      height: 32,
      left: 420,
      right: 800,
      width: 380,
      x: 420,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')

    expect(wrapper.find('.pinned-columns').attributes('style')).toContain('420px')
    expect(wrapper.find('.columns-header').attributes('style')).toContain('380px')
    expect(wrapper.find('.columns-header').attributes('style')).not.toContain('1fr')
  })

  it('全部服务都固定后固定区占满内容区，且不被后续窄栏测量覆盖', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 2, 'svc-server': 2 }
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger payload', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server payload', '2026-05-20T14:41:44.501Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const initialHeaders = wrapper.findAll('.columns .column-header')
    vi.spyOn(initialHeaders[0].element, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      bottom: 32,
      height: 32,
      left: 0,
      right: 420,
      width: 420,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)
    vi.spyOn(initialHeaders[1].element, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      bottom: 32,
      height: 32,
      left: 420,
      right: 800,
      width: 380,
      x: 420,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')

    const remainingHeader = wrapper.find('.columns .column-header').element
    vi.spyOn(remainingHeader, 'getBoundingClientRect').mockReturnValue({
      top: 0,
      bottom: 32,
      height: 32,
      left: 0,
      right: 40,
      width: 40,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    await wrapper.find('.columns .pin-btn').trigger('click')

    expect(wrapper.find('.columns-shell').classes()).toContain('all-pinned')
    expect(wrapper.find('.columns.pinned-only').exists()).toBe(false)
    expect(wrapper.find('.pinned-columns').attributes('style')).toContain('--pinned-width: 800px')
    expect(wrapper.find('.pinned-columns').attributes('style')).toContain('420px 380px')
    expect(wrapper.findAll('.pinned-column')).toHaveLength(2)
  })

  it('固定当前选中服务时不会让布局变化产生的滚动事件改写高亮', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-logger': 3568, 'svc-server': 2 }
    tab.results = [
      log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      log(40, 'svc-server', 'server trace', '2026-05-20T14:41:44.401Z'),
    ]
    tab.selectedLogId = 20
    tab.contextAnchorTime = '2026-05-20T14:41:43.401Z'
    tab.contextByService = {
      'svc-logger': [
        log(20, 'svc-logger', 'logger trace', '2026-05-20T14:41:43.401Z'),
      ],
      'svc-server': [
        log(40, 'svc-server', 'server trace', '2026-05-20T14:41:44.401Z'),
      ],
    }

    const wrapper = mount(SearchServiceColumns, {
      props: { tabId: tab.id },
    })
    const columns = wrapper.find('.columns').element as HTMLElement
    Object.defineProperty(columns, 'scrollTop', { value: 300, writable: true, configurable: true })
    Object.defineProperty(columns, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(columns, 'scrollHeight', { value: 1200, configurable: true })

    await wrapper.findAll('.columns .pin-btn')[0].trigger('click')
    setRect(columns, 0, 400)
    setRect(wrapper.find('[data-entry-id="40"]').element, 180, 204)

    await wrapper.find('.columns').trigger('scroll')

    expect(tab.selectedLogId).toBe(20)
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
