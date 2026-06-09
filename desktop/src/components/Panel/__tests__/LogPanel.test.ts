/**
 * LogPanel 组件测试
 *
 * 职责：
 *   - 验证 deployment 来源变化时日志订阅生命周期正确切换
 *
 * 边界：
 *   - 不建立真实 WebSocket/HTTP 连接，订阅与历史加载通过 store spy 验证
 *   - 不测试虚拟列表渲染细节，useVirtualizer 使用轻量 mock
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, ref } from 'vue'
import LogPanel from '../LogPanel.vue'
import { api } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { useDeploymentNodeSelectionStore } from '@/stores/deploymentNodeSelection'
import { useFilterStore } from '@/stores/filter'
import { installTestI18n } from '@/test-utils/i18n'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { Project } from '@/api/agent'

const virtualizerMock = vi.hoisted(() => ({
  scrollToIndex: vi.fn(),
  measure: vi.fn(),
  measureElement: vi.fn(),
  getVirtualItems: vi.fn(() => []),
  getTotalSize: vi.fn(() => 0),
  range: { startIndex: 0 },
  optionsRef: null as any,
}))

vi.mock('@tanstack/vue-virtual', () => ({
  useVirtualizer: (options: any) => {
    virtualizerMock.optionsRef = options
    const virtualizer = {
      getTotalSize: virtualizerMock.getTotalSize,
      getVirtualItems: virtualizerMock.getVirtualItems,
      scrollToIndex: virtualizerMock.scrollToIndex,
      measure: virtualizerMock.measure,
      measureElement: virtualizerMock.measureElement,
      range: virtualizerMock.range,
    }
    return { ...virtualizer, value: virtualizer }
  },
}))

function makeLog(id: number): DisplayLogEntry {
  return {
    id: String(id),
    deployment_id: 'dep-1',
    run_id: 'run-1',
    timestamp: `2026-05-30T10:00:${String(id).padStart(2, '0')}.000Z`,
    level: 'INFO',
    message: `log-${id}`,
    stream: 'stdout',
    repeat_count: 1,
  }
}

describe('LogPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    virtualizerMock.range.startIndex = 0
    virtualizerMock.optionsRef = null
  })

  it('source 切换到另一个 deployment 时重新订阅并加载历史', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const subscribe = vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    const unsubscribe = vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    const loadMoreHistory = vi
      .spyOn(deploymentLogStore, 'loadMoreHistory')
      .mockResolvedValue({ added: 0, entries: [] })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-1',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })
    await nextTick()

    expect(subscribe).toHaveBeenCalledWith('dep-1')
    expect(loadMoreHistory).toHaveBeenCalledWith('dep-1', 200)

    await wrapper.setProps({
      source: { type: 'deployment', deploymentId: 'dep-2' },
    })
    await nextTick()

    expect(unsubscribe).toHaveBeenCalledWith('dep-1')
    expect(subscribe).toHaveBeenCalledWith('dep-2')
    expect(loadMoreHistory).toHaveBeenCalledWith('dep-2', 200)
  })

  it('首次历史加载完成后刷新显示列表以插入历史分隔线', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([
      {
        id: '7',
        deployment_id: 'dep-1',
        run_id: 'run-1',
        timestamp: '2026-05-30T10:00:00.000Z',
        level: 'INFO',
        message: 'history',
        stream: 'stdout',
        repeat_count: 1,
      },
    ])
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({
      added: 1,
      entries: [],
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-1',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div data-test="history-separator" />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()

    expect(wrapper.exists()).toBe(true)
    expect(deploymentLogStore.loadMoreHistory).toHaveBeenCalledWith('dep-1', 200)
  })

  it('向上加载历史后保持原可见行位置并使用较小页大小', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const logs = ref([makeLog(10), makeLog(11), makeLog(12)])
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'hasMoreHistory').mockReturnValue(true)
    vi.spyOn(deploymentLogStore, 'getLogs').mockImplementation(() => logs.value)
    const loadMoreHistory = vi
      .spyOn(deploymentLogStore, 'loadMoreHistory')
      .mockImplementation(async (_deploymentId, limit = 200) => {
        if (limit < 200) {
          logs.value = [makeLog(7), makeLog(8), makeLog(9), ...logs.value]
          return { added: 3, entries: [] }
        }
        return { added: 0, entries: [] }
      })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-1',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 100))
    loadMoreHistory.mockClear()
    virtualizerMock.scrollToIndex.mockClear()
    virtualizerMock.range.startIndex = 2

    const el = wrapper.find('.log-list').element
    Object.defineProperty(el, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 600, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 390, configurable: true })

    await wrapper.find('.log-list').trigger('scroll')
    await Promise.resolve()
    await nextTick()
    await Promise.resolve()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(loadMoreHistory).toHaveBeenCalledWith('dep-1', 80)
    expect(virtualizerMock.scrollToIndex).toHaveBeenCalledWith(5, { align: 'start' })
  })

  it('向顶部插入历史时用稳定条目 id 作为虚拟行 key 并重新测量', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const logs = ref([makeLog(10), makeLog(11), makeLog(12)])
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'hasMoreHistory').mockReturnValue(true)
    vi.spyOn(deploymentLogStore, 'getLogs').mockImplementation(() => logs.value)
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockImplementation(async (_deploymentId, limit = 200) => {
      if (limit < 200) {
        logs.value = [makeLog(7), makeLog(8), makeLog(9), ...logs.value]
        return { added: 3, entries: [] }
      }
      return { added: 0, entries: [] }
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-1',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()

    expect(virtualizerMock.optionsRef.value.getItemKey(0)).toBe('live-10')

    await new Promise(resolve => setTimeout(resolve, 100))
    virtualizerMock.measure.mockClear()
    virtualizerMock.range.startIndex = 2

    const el = wrapper.find('.log-list').element
    Object.defineProperty(el, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 600, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 390, configurable: true })

    await wrapper.find('.log-list').trigger('scroll')
    await Promise.resolve()
    await nextTick()
    await Promise.resolve()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(virtualizerMock.measure).toHaveBeenCalled()
    expect(virtualizerMock.optionsRef.value.getItemKey(0)).toBe('live-7')
  })

  it('切换临时过滤 AND/OR 后立即刷新显示统计', async () => {
    const filterStore = useFilterStore()
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([
      { ...makeLog(1), message: 'error only' },
      { ...makeLog(2), message: 'timeout only' },
      { ...makeLog(3), message: 'error timeout' },
    ])

    filterStore.addChip('panel-filter', 'error', 'include')
    filterStore.addChip('panel-filter', 'timeout', 'include')

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-filter',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()

    expect(wrapper.find('[data-test="log-panel-status"]').text()).toContain('3')

    filterStore.toggleLogic('panel-filter')
    await nextTick()
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))
    await nextTick()

    expect(wrapper.find('[data-test="log-panel-status"]').text()).toContain('1')
  })

  it('英文 locale 下渲染状态栏文案', async () => {
    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-1',
        projectId: null,
        source: null,
      },
      global: {
        plugins: [installTestI18n('en-US')],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()

    expect(wrapper.text()).toContain('Live · showing 0')
  })

  it('远端 deployment 日志 tab 展示节点筛选条并同步节点选择', async () => {
    vi.spyOn(api, 'listHosts').mockResolvedValue([
      { id: 'h1', name: 'ali-01', private_ip: '10.0.0.1', tags: [] },
      { id: 'h2', name: 'jp', private_ip: '10.0.0.2', tags: [] },
    ])
    vi.spyOn(api, 'getHostManagedDeploymentStatus').mockImplementation(async (hostId: string) => ({
      host_id: hostId,
      host_name: hostId,
      desired_deployment_count: 1,
      desired_collector_count: 1,
      tunnel_connected: true,
      remote: {
        deployment_count: 1,
        collector_count: 1,
        collectors: [{
          deployment_id: 'dep-api',
          desired: true,
          running: true,
          status: 'running',
        }],
      },
    }))
    vi.spyOn(api, 'getProjectRules').mockResolvedValue([])
    const agentStore = useAgentStore()
    const deploymentLogStore = useDeploymentLogStore()
    const nodeSelectionStore = useDeploymentNodeSelectionStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    const project: Project = {
      id: 'proj-1',
      name: 'Project',
      root_path: '/tmp/project',
      env_selected_service_ids: {},
      environments: [{ id: 'env-prod', name: 'prod', is_dev: false, order: 0 }],
      services: [{
        id: 'svc-api',
        project_id: 'proj-1',
        name: 'api',
        status: 'running',
        required: false,
        order: 1,
        deployments: [{
          id: 'dep-api',
          env_name: 'prod',
          location: 'remote',
          status: 'running',
          host_ids: ['h1', 'h2'],
          logs: { type: 'file_tail', path: '/var/log/api.log' },
        }],
      }],
    }
    agentStore.projects = [project]

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-remote',
        projectId: 'proj-1',
        source: { type: 'deployment', deploymentId: 'dep-api' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()

    expect(wrapper.findAll('[data-test="log-node-filter-chip"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="log-node-filter-strip"]').text()).toContain('节点 2/2')

    await wrapper.findAll('[data-test="log-node-filter-chip"]')[1].trigger('click')

    expect(nodeSelectionStore.selectedHostIds('dep-api')).toEqual(['h1'])
    expect(wrapper.find('[data-test="log-node-filter-strip"]').text()).toContain('节点 1/2')
  })
})
