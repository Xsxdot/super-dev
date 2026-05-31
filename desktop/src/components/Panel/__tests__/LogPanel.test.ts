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
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import type { DisplayLogEntry } from '@/lib/logEngine'

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
    id,
    deployment_id: 'dep-1',
    run_id: 'run-1',
    timestamp: `2026-05-30T10:00:${String(id).padStart(2, '0')}.000Z`,
    level: 'INFO',
    message: `log-${id}`,
    stream: 'stdout',
    normalized_message: `log-${id}`,
    repeat_count: 1,
  }
}

describe('LogPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
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
        id: 7,
        deployment_id: 'dep-1',
        run_id: 'run-1',
        timestamp: '2026-05-30T10:00:00.000Z',
        level: 'INFO',
        message: 'history',
        stream: 'stdout',
        normalized_message: 'history',
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
})
