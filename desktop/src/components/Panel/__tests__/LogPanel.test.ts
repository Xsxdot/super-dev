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
import { useLogEvidenceStore } from '@/stores/logEvidence'
import { installTestI18n } from '@/test-utils/i18n'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { Project } from '@/api/agent'

const virtualizerMock = vi.hoisted(() => ({
  scrollToIndex: vi.fn(),
  measure: vi.fn(),
  measureElement: vi.fn(),
  getVirtualItems: vi.fn((): ReturnType<typeof virtualRow>[] => []),
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

function virtualRow(index: number) {
  return { key: `live-${index}`, index, start: index * 22, size: 22, end: index * 22 + 22, lane: 0 }
}

function setVirtualRows(...indexes: number[]) {
  virtualizerMock.getVirtualItems.mockReturnValue(indexes.map(virtualRow))
}

function stubEvidenceLogRow() {
  return {
    props: ['log', 'serviceName', 'highlighted', 'evidencePin', 'evidenceFlash', 'timeAnchor'],
    emits: ['selection-change', 'toggle-pin', 'edit-pin', 'row-context-menu'],
    template: `
      <button
        type="button"
        data-test="evidence-log-row"
        :data-pin="evidencePin?.label || ''"
        :data-flash="evidenceFlash ? 'yes' : 'no'"
        @dblclick="$emit('toggle-pin', log)"
        @contextmenu.prevent="$emit('row-context-menu', $event, log)"
      >
        <span
          v-if="evidencePin"
          data-test="pin-edit-trigger"
          @click.stop="$emit('edit-pin', evidencePin, $event)"
        >pin</span>
        {{ log.message }}
      </button>
    `,
  }
}

describe('LogPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    virtualizerMock.range.startIndex = 0
    virtualizerMock.optionsRef = null
    setVirtualRows()
    virtualizerMock.getTotalSize.mockReturnValue(0)
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
    expect(virtualizerMock.scrollToIndex).toHaveBeenCalledWith(5, { align: 'center' })
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

  it('过滤后不足一屏时向上滚轮会显示加载状态并跳过被过滤的历史页', async () => {
    const filterStore = useFilterStore()
    const deploymentLogStore = useDeploymentLogStore()
    const logs = ref([{ ...makeLog(30), message: 'keep newest' }])
    let hasMore = true
    const hiddenPage = { resolve: undefined as undefined | (() => void) }
    const releaseHiddenPage = () => {
      if (!hiddenPage.resolve) throw new Error('hidden history page load was not started')
      hiddenPage.resolve()
    }
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'hasMoreHistory').mockImplementation(() => hasMore)
    vi.spyOn(deploymentLogStore, 'getLogs').mockImplementation(() => logs.value)
    const loadMoreHistory = vi
      .spyOn(deploymentLogStore, 'loadMoreHistory')
      .mockImplementation(async (_deploymentId, limit = 200) => {
        if (limit === 200) return { added: 0, entries: [] }
        if (loadMoreHistory.mock.calls.filter(call => call[1] === 80).length === 1) {
          await new Promise<void>(resolve => { hiddenPage.resolve = resolve })
          logs.value = [
            { ...makeLog(28), message: 'noise older' },
            { ...makeLog(29), message: 'noise middle' },
            ...logs.value,
          ]
          return { added: 2, entries: [] }
        }
        hasMore = false
        logs.value = [
          { ...makeLog(27), message: 'keep older' },
          ...logs.value,
        ]
        return { added: 1, entries: [] }
      })
    filterStore.addChip('panel-filter-backfill', 'keep', 'include')

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-filter-backfill',
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
    await new Promise(resolve => setTimeout(resolve, 0))
    loadMoreHistory.mockClear()

    await wrapper.find('.log-list').trigger('wheel', { deltaY: -120 })
    await nextTick()
    await Promise.resolve()

    expect(wrapper.find('.history-loading').exists()).toBe(true)
    expect(loadMoreHistory).toHaveBeenCalledTimes(1)

    releaseHiddenPage()
    await Promise.resolve()
    await Promise.resolve()
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(loadMoreHistory).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="log-panel-status"]').text()).toContain('显示 2 条')
    expect(wrapper.find('.history-loading').exists()).toBe(false)
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

  it('底部状态栏显示被过滤规则隐藏的日志数量', async () => {
    const filterStore = useFilterStore()
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([
      { ...makeLog(1), message: 'debug noise' },
      { ...makeLog(2), message: 'error signal' },
    ])
    filterStore.addChip('panel-filter-count', 'debug', 'exclude')

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-filter-count',
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
    await new Promise(resolve => setTimeout(resolve, 0))
    await nextTick()

    const status = wrapper.find('[data-test="log-panel-status"]').text()
    expect(status).toContain('显示 1 条')
    expect(status).toContain('已过滤 1 条')
  })

  it('follow 模式下项目规则收窄日志数量时重新测量并贴到新底部', async () => {
    const filterStore = useFilterStore()
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([
      { ...makeLog(1), message: 'keep signal' },
      { ...makeLog(2), message: 'drop noise' },
      { ...makeLog(3), message: 'drop noise too' },
    ])
    vi.spyOn(api, 'getProjectRules').mockResolvedValue([])
    filterStore.projectRules['proj-1'] = []

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-project-rule-shrink',
        projectId: 'proj-1',
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
    virtualizerMock.scrollToIndex.mockClear()
    virtualizerMock.measure.mockClear()

    filterStore.projectRules['proj-1'] = [{
      id: 'rule-keep',
      name: 'BASE',
      type: 'include',
      keywords: ['keep'],
      logic: 'or',
      enabled: true,
    }]
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 40))
    await nextTick()

    expect(wrapper.find('[data-test="log-panel-status"]').text()).toContain('显示 1 条')
    expect(virtualizerMock.measure).toHaveBeenCalled()
    expect(virtualizerMock.scrollToIndex).toHaveBeenCalledWith(
      virtualizerMock.optionsRef.value.count - 1,
      { align: 'end' },
    )
  })

  it('用户离开底部时通过 ScrollIntent 仲裁器发出意图转移诊断', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1), makeLog(2)])
    const diagnostics: string[] = []
    window.addEventListener('superdev:log-panel', (event) => {
      diagnostics.push((event as CustomEvent).detail.event)
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-scroll-intent',
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

    const el = wrapper.find('.log-list').element
    Object.defineProperty(el, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 500, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 100, configurable: true })

    await wrapper.find('.log-list').trigger('scroll')

    expect(diagnostics).toContain('scroll_intent.transition')
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
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([
      { ...makeLog(1), deployment_id: 'dep-api', message: 'from h1', source_id: 'h1' },
      { ...makeLog(2), deployment_id: 'dep-api', message: 'from h2', source_id: 'h2' },
    ])
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

    const status = wrapper.find('[data-test="log-panel-status"]').text()
    expect(status).toContain('显示 1 条')
    expect(status).not.toContain('已过滤')
  })

  it('注册并注销当前日志面板的 evidence track', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const evidenceStore = useLogEvidenceStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1)])
    const registerTrack = vi.spyOn(evidenceStore, 'registerTrack')
    const unregisterTrack = vi.spyOn(evidenceStore, 'unregisterTrack')

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-evidence',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: stubEvidenceLogRow(),
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
          LogContextMenu: { template: '<div />' },
        },
      },
    })

    await nextTick()

    expect(registerTrack).toHaveBeenCalledWith(expect.objectContaining({
      trackId: 'panel-evidence',
      panelId: 'panel-evidence',
      sourceKey: 'dep-1',
    }))

    wrapper.unmount()

    expect(unregisterTrack).toHaveBeenCalledWith('panel-evidence')
  })

  it('双击日志行时在当前 track 添加证据钉子', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const evidenceStore = useLogEvidenceStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1)])
    setVirtualRows(0)
    virtualizerMock.getTotalSize.mockReturnValue(22)

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-pin',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: stubEvidenceLogRow(),
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
          LogContextMenu: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()

    await wrapper.find('[data-test="evidence-log-row"]').trigger('dblclick')

    expect(evidenceStore.pins.map(pin => `${pin.trackId}:${pin.logId}`)).toEqual(['panel-pin:1'])
  })

  it('右键菜单可以复制带 cursor 的日志', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1)])
    setVirtualRows(0)
    virtualizerMock.getTotalSize.mockReturnValue(22)
    Object.assign(navigator, { clipboard: { writeText: vi.fn() } })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-copy',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: stubEvidenceLogRow(),
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
          LogContextMenu: {
            emits: ['copy-log-with-cursor'],
            template: '<button data-test="copy-with-cursor" @click="$emit(\'copy-log-with-cursor\')">copy</button>',
          },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()
    await wrapper.find('[data-test="evidence-log-row"]').trigger('contextmenu')
    await nextTick()
    await wrapper.find('[data-test="copy-with-cursor"]').trigger('click')

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('cursor_id: 1'))
  })

  it('store jumpToPin 会滚动到对应日志并闪烁该行', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const evidenceStore = useLogEvidenceStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1)])
    setVirtualRows(0)
    virtualizerMock.getTotalSize.mockReturnValue(22)

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-jump',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: stubEvidenceLogRow(),
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
          LogContextMenu: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()
    const pin = evidenceStore.addPin({
      panelId: 'panel-jump',
      trackId: 'panel-jump',
      trackLabel: 'dep-1',
      sourceKey: 'dep-1',
      log: makeLog(1),
    })

    await evidenceStore.jumpToPin(pin.id)
    await nextTick()

    expect(virtualizerMock.scrollToIndex).toHaveBeenCalledWith(0, { align: 'center' })
    expect(wrapper.find('[data-test="evidence-log-row"]').attributes('data-flash')).toBe('yes')
  })

  it('点击钉子可以补充备注并写回证据 store', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    const evidenceStore = useLogEvidenceStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1)])
    setVirtualRows(0)
    virtualizerMock.getTotalSize.mockReturnValue(22)

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-note',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: stubEvidenceLogRow(),
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
          LogContextMenu: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()
    await wrapper.find('[data-test="evidence-log-row"]').trigger('dblclick')
    await nextTick()
    await wrapper.find('[data-test="pin-edit-trigger"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-test="pin-note-popover"]').attributes('style')).toContain('left:')
    await wrapper.find('[data-test="pin-note-input"]').setValue('need agent check this spike')
    await wrapper.find('[data-test="pin-note-save"]').trigger('click')

    expect(evidenceStore.pins[0]?.note).toBe('need agent check this spike')
    expect(wrapper.find('[data-test="pin-note-popover"]').exists()).toBe(false)
  })
})
