/**
 * BottomBar 组件测试
 *
 * 职责：
 *   - 验证底部栏面板服务默认选中
 *   - 验证同步录制可开始、停止并产出可复制/导出的同步书签
 *
 * 边界：
 *   - 不测试 Tauri 文件对话框真实行为
 *   - 不建立 WebSocket 连接，日志通过 logStore 直接注入
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import BottomBar from '../BottomBar.vue'
import { useAgentStore } from '../../stores/agent'
import { useBookmarkStore } from '../../stores/bookmark'
import { useLogStore } from '../../stores/log'
import { usePanelStore } from '../../stores/panel'
import { toDisplayEntry } from '../../lib/logEngine'
import type { LogEntry, Project, Service } from '../../api/agent'

function makeService(id: string, name: string): Service {
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

function makeProject(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

function makeLog(serviceId: string, message: string, timestamp: string): LogEntry {
  return {
    id: 101,
    service_id: serviceId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

async function mountBottomBarWithServices() {
  const panelStore = usePanelStore()
  const agentStore = useAgentStore()
  const api = makeService('svc-api', 'api')
  const worker = makeService('svc-worker', 'worker')
  agentStore.projects = [makeProject([api, worker])]

  const firstPanelId = panelStore.root.id
  panelStore.replaceScope(firstPanelId, api.id, api.project_id)
  panelStore.splitLeaf(firstPanelId, 'h', worker.id, worker.project_id, 'second')

  const wrapper = mount(BottomBar)
  await nextTick()
  return { wrapper, panelStore, api, worker }
}

describe('BottomBar', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.useFakeTimers()
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn(),
      },
    })
  })

  it('面板服务首次出现时默认选中', async () => {
    const { wrapper } = await mountBottomBarWithServices()
    const serviceChecks = wrapper.findAll('.service-chip input[type="checkbox"]')

    expect(serviceChecks).toHaveLength(2)
    expect(serviceChecks.every(input => (input.element as HTMLInputElement).checked)).toBe(true)
  })

  it('同步录制开始时登记面板和服务，停止后显示复制导出入口', async () => {
    const { wrapper, panelStore, api } = await mountBottomBarWithServices()
    const bookmarkStore = useBookmarkStore()
    const logStore = useLogStore()
    const apiPanel = panelStore.allLeaves.find(leaf => leaf.serviceId === api.id)!

    await wrapper.find('.sync-label input[type="checkbox"]').setValue(true)
    await wrapper.find('.sync-record-btn').trigger('click')

    expect(bookmarkStore.syncPanelIds.has(apiPanel.id)).toBe(true)
    expect(bookmarkStore.getBookmark(apiPanel.id)?.serviceId).toBe(api.id)
    expect(bookmarkStore.syncRecording).toBe(true)

    const start = bookmarkStore.getBookmark(apiPanel.id)!.startTime!
    const ts = new Date(start.getTime() + 1000).toISOString()
    logStore.serviceLogs[api.id] = {
      logs: [toDisplayEntry(makeLog(api.id, 'sync captured', ts))],
      ws: null,
      refCount: 1,
      bootstrapPromise: null,
      historyBoundary: null,
      oldestLoadedId: null,
      hasMoreHistory: true,
      loadingMoreHistory: false,
      seenSignatures: new Set(),
    }
    vi.advanceTimersByTime(5000)

    await wrapper.find('.sync-record-btn').trigger('click')

    expect(bookmarkStore.syncRecording).toBe(false)
    expect(bookmarkStore.getBookmark(apiPanel.id)?.lockedLogs.map(l => l.message)).toEqual([
      'sync captured',
    ])
    expect(wrapper.find('.sync-copy-btn').exists()).toBe(true)
    expect(wrapper.find('.sync-export-btn').exists()).toBe(true)
  })
})
