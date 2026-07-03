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

    expect(subscribe).toHaveBeenCalledWith('dep-1', null)
    expect(loadMoreHistory).toHaveBeenCalledWith('dep-1', 200)

    await wrapper.setProps({
      source: { type: 'deployment', deploymentId: 'dep-2' },
    })
    await nextTick()

    expect(unsubscribe).toHaveBeenCalledWith('dep-1')
    expect(subscribe).toHaveBeenCalledWith('dep-2', null)
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

  it('wheel 上滚立即离开 follow 并发出意图转移诊断', async () => {
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
    Object.defineProperty(el, 'scrollTop', { value: 500, configurable: true })
    await wrapper.find('.log-list').trigger('wheel', { deltaY: -100 })

    expect(diagnostics).toContain('scroll_intent.transition')
  })

  it('突发日志导致 scrollHeight 暴涨时 follow 不脱落', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    const logs = [makeLog(1), makeLog(2)]
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue(logs)
    const transitions: string[] = []
    window.addEventListener('superdev:log-panel', (event) => {
      const detail = (event as CustomEvent).detail
      if (detail.event === 'scroll_intent.transition') transitions.push(detail.to)
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-follow-burst',
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
    const el = wrapper.find('.log-list').element
    Object.defineProperty(el, 'scrollHeight', { value: 2000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 500, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 100, configurable: true })
    await wrapper.find('.log-list').trigger('scroll')
    await wrapper.find('.log-list').trigger('scroll')

    logs.push(makeLog(3))
    deploymentLogStore.logSourceRevision++
    await new Promise(resolve => setTimeout(resolve, 60))
    await nextTick()

    expect(transitions).not.toContain('idle')
    expect(wrapper.find('.new-log-pill').exists()).toBe(false)
  })

  it('wheel 下滚到底部附近恢复 follow 并隐藏新日志提示', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    const logs = [makeLog(1), makeLog(2)]
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue(logs)
    const transitions: string[] = []
    window.addEventListener('superdev:log-panel', (event) => {
      const detail = (event as CustomEvent).detail
      if (detail.event === 'scroll_intent.transition') transitions.push(detail.to)
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-wheel-bottom',
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
    const list = wrapper.find('.log-list')
    const el = list.element
    Object.defineProperty(el, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 500, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 100, configurable: true })
    await list.trigger('wheel', { deltaY: -100 })

    logs.push(makeLog(3))
    deploymentLogStore.logSourceRevision++
    await new Promise(resolve => setTimeout(resolve, 60))
    await nextTick()
    expect(transitions).toContain('idle')

    Object.defineProperty(el, 'scrollTop', { value: 450, configurable: true })
    await list.trigger('wheel', { deltaY: 100 })

    expect(transitions).toContain('follow-bottom')
    expect(wrapper.find('.new-log-pill').exists()).toBe(false)
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
      workspaceTabId: 'default',
      trackId: 'panel-evidence',
      panelId: 'panel-evidence',
      sourceKey: 'dep-1',
    }))

    wrapper.unmount()

    expect(unregisterTrack).toHaveBeenCalledWith('panel-evidence', 'default')
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
      workspaceTabId: 'default',
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

  it('idle 状态下实时新日志到达不主动 measure 或滚动（防止把用户视口拉走）', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    const logs = [makeLog(1), makeLog(2)]
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue(logs)

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-idle-live',
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

    // 用户 wheel 上滚离开底部 → 进入 idle
    const el = wrapper.find('.log-list').element
    Object.defineProperty(el, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 500, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 100, configurable: true })
    await wrapper.find('.log-list').trigger('wheel', { deltaY: -100 })

    // 清掉进入 idle 过程中的调用，只观察「之后实时日志到达」的影响
    virtualizerMock.measure.mockClear()
    virtualizerMock.scrollToIndex.mockClear()

    // 实时新日志到达：追加一条并 bump revision，触发 commitDisplay('content')
    logs.push(makeLog(3))
    deploymentLogStore.logSourceRevision++
    await new Promise(resolve => setTimeout(resolve, 60)) // 越过 32ms 防抖
    await nextTick()

    // idle 下实时增量不得主动 measure / 滚动，否则视口被持续拉走
    expect(virtualizerMock.scrollToIndex).not.toHaveBeenCalled()
    expect(virtualizerMock.measure).not.toHaveBeenCalled()
  })

  it('follow-bottom 状态下被过滤/折叠的新日志（可见行不变）不主动 measure（防止视口上跳）', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    // 可见行固定为 2 条；模拟「被过滤掉/折叠增量」——getLogs 不变，仅 revision 自增
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1), makeLog(2)])

    mount(LogPanel, {
      props: {
        panelId: 'panel-follow-fold',
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

    // 保持在底部（follow-bottom，默认态），清掉初始化期间的调用
    virtualizerMock.measure.mockClear()
    virtualizerMock.scrollToIndex.mockClear()

    // 被过滤/折叠的新日志到达：可见行集合不变，仅 revision 自增
    deploymentLogStore.logSourceRevision++
    await new Promise(resolve => setTimeout(resolve, 60)) // 越过 32ms 防抖
    await nextTick()

    // 可见行未变 → 不需要贴底，也就不该 measure（否则 _scrollToOffset 把视口上带）
    expect(virtualizerMock.measure).not.toHaveBeenCalled()
    expect(virtualizerMock.scrollToIndex).not.toHaveBeenCalled()
  })

  it('贴底时在行高异步测量沉降前逐帧重新断言底部（totalSize 持续增长则反复滚到底）', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 1, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1), makeLog(2), makeLog(3)])

    // 模拟行高异步测量：前几帧 totalSize 持续增大（底部真实高度逐步上报），随后沉降不变。
    // 单次 scrollToIndex 会落在更小的估算底部之上；正确实现必须每帧重滚直到 totalSize 稳定。
    let totalSize = 60
    virtualizerMock.getTotalSize.mockImplementation(() => {
      const current = totalSize
      if (totalSize < 300) totalSize += 80
      return current
    })

    mount(LogPanel, {
      props: {
        panelId: 'panel-settle-bottom',
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
    // 给 rAF 沉降循环留足时间（前几帧增长，之后稳定）
    await new Promise(resolve => setTimeout(resolve, 200))

    // 贴底统一用 align:'end' 滚到最后一条；沉降前每帧重滚一次。
    const bottomCalls = virtualizerMock.scrollToIndex.mock.calls.filter(
      ([, opts]) => opts?.align === 'end',
    )
    // 单帧只滚一次会落不到真实底部；沉降前必须多帧重滚（至少 2 次）。
    expect(bottomCalls.length).toBeGreaterThanOrEqual(2)
  })

  it('贴底 settle 结束后 scrollToIndex 留下的滞后 scroll 事件不得把 follow-bottom 误判为 idle', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([makeLog(1), makeLog(2), makeLog(3)])
    virtualizerMock.getTotalSize.mockReturnValue(66)

    const diagnostics: Array<{ from: string; to: string }> = []
    window.addEventListener('superdev:log-panel', (event) => {
      const detail = (event as CustomEvent).detail
      if (detail.event === 'scroll_intent.transition') {
        diagnostics.push({ from: detail.from, to: detail.to })
      }
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-lagging-scroll',
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
    // 给初始 scrollToBottom 的 rAF settle 循环跑完
    await new Promise(resolve => setTimeout(resolve, 100))

    // 模拟浏览器滞后派发的 scroll 事件：scrollToIndex 已把视口贴底，但突发新日志让
    // scrollHeight 在这一刻已经增大，scrollTop 还是贴底前一刻的值 → distanceFromBottom 偏大。
    // 这是程序化贴底留下的尾迹，不是用户真正向上滚，绝不能转入 idle。
    // 突发日志：底部行逐帧测量使 scrollHeight 暴涨到 1200，但 scrollTop 仍停在贴底前的 600，
    // distanceFromBottom = 1200 - 600 - 600 = 0... 这里刻意制造一个偏大的瞬时距离：
    // scrollHeight 已增大但 scrollTop 尚未被浏览器更新到新底部，dist 偏大却非用户上滚。
    const el = wrapper.find('.log-list').element
    Object.defineProperty(el, 'scrollHeight', { value: 1200, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 600, configurable: true })
    Object.defineProperty(el, 'scrollTop', { value: 300, configurable: true })

    await wrapper.find('.log-list').trigger('scroll')

    // 滞后的程序化滚动尾迹（scrollTop 未向上移动，仅 scrollHeight 增大）
    // 不应触发 follow-bottom → idle 的意图转移
    expect(diagnostics.some(d => d.to === 'idle')).toBe(false)
  })
})
