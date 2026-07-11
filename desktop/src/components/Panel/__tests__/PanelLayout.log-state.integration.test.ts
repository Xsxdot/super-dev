/**
 * PanelLayout 日志状态隔离集成测试。
 *
 * 职责：
 *   - 挂载真实 PanelLayout、PanelLeaf、LogPanel 与 TanStack Virtual
 *   - 验证长列表切换到另一 workspace leaf 后，新面板从空本地缓存建立显示状态
 *
 * 边界：
 *   - 日志订阅与历史请求通过 store spy 隔离，不建立真实网络连接
 *   - 行内容组件使用轻量 stub；组件身份、LogPanel 状态和 virtualizer 均使用真实实现
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import PanelLayout from '../PanelLayout.vue'
import type { DisplayLogEntry } from '@/lib/logEngine'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { usePanelStore } from '@/stores/panel'
import { installTestI18n } from '@/test-utils/i18n'

function makeLogs(deploymentId: string, count: number): DisplayLogEntry[] {
  return Array.from({ length: count }, (_, index) => ({
    id: String(index + 1),
    deployment_id: deploymentId,
    run_id: `run-${deploymentId}`,
    timestamp: new Date(Date.UTC(2026, 6, 11, 10, 0, 0, index)).toISOString(),
    level: 'INFO',
    message: `${deploymentId}-log-${index + 1}`,
    stream: 'stdout',
    repeat_count: 1,
  }))
}

async function flushLogPanelUpdates() {
  await nextTick()
  await Promise.resolve()
  await nextTick()
  await new Promise(resolve => setTimeout(resolve, 60))
  await nextTick()
}

describe('PanelLayout log state isolation', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('starts the new leaf at oldCount zero when switching from a long list to a short list', async () => {
    const panelStore = usePanelStore()
    const deploymentLogStore = useDeploymentLogStore()
    const logsByDeployment: Record<string, DisplayLogEntry[]> = {
      'dep-long': makeLogs('dep-long', 471),
      'dep-short': makeLogs('dep-short', 3),
    }
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({ added: 0, entries: [] })
    vi.spyOn(deploymentLogStore, 'getLogs').mockImplementation(
      deploymentId => logsByDeployment[deploymentId] ?? [],
    )
    panelStore.setRoot({
      type: 'leaf',
      id: 'leaf-long',
      serviceId: 'dep-long',
      projectId: null,
      source: { type: 'deployment', deploymentId: 'dep-long' },
    })

    const countChanges: Array<{ oldCount: number; newCount: number }> = []
    const recordCountChange = (event: Event) => {
      const detail = (event as CustomEvent).detail
      if (detail?.event !== 'virtualizer.count_change') return
      countChanges.push({ oldCount: detail.oldCount, newCount: detail.newCount })
    }
    window.addEventListener('superdev:log-panel', recordCountChange)

    const wrapper = mount(PanelLayout, {
      global: {
        plugins: [installTestI18n()],
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
          LogGapSeparatorRow: { template: '<div />' },
          LogContextMenu: { template: '<div />' },
          PinNotePopover: { template: '<div />' },
        },
      },
    })

    try {
      await flushLogPanelUpdates()
      expect(wrapper.find('[data-test="log-panel-status"]').text()).toContain('471')
      countChanges.length = 0

      panelStore.setRoot({
        type: 'leaf',
        id: 'leaf-short',
        serviceId: 'dep-short',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-short' },
      })
      await flushLogPanelUpdates()

      expect(wrapper.find('[data-test="log-panel-status"]').text()).toContain('3')
      expect(countChanges).toContainEqual({ oldCount: 0, newCount: 3 })
      expect(countChanges.some(change => change.oldCount === 471)).toBe(false)
    } finally {
      window.removeEventListener('superdev:log-panel', recordCountChange)
      wrapper.unmount()
    }
  })
})
