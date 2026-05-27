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
import { flushPromises, mount } from '@vue/test-utils'
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

const tauriMocks = vi.hoisted(() => ({
  save: vi.fn(),
  writeTextFile: vi.fn(),
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  save: tauriMocks.save,
}))

vi.mock('@tauri-apps/plugin-fs', () => ({
  writeTextFile: tauriMocks.writeTextFile,
}))

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
    vi.clearAllMocks()
    vi.useFakeTimers()
    tauriMocks.save.mockResolvedValue('/tmp/sync-export')
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

  it('同步录制按面板级 source 快照覆盖本地与远程且面板服务只包含本地服务', async () => {
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const api = makeService('svc-api', 'api')
    agentStore.projects = [makeProject([api])]
    panelStore.setRoot(
      {
        type: 'split',
        id: 'split-1',
        axis: 'h',
        ratio: 0.5,
        first: {
          type: 'leaf',
          id: 'panel-local',
          source: { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-api' },
        },
        second: {
          type: 'leaf',
          id: 'panel-remote',
          source: { type: 'remote-log-source', logSourceId: 'remote-prod', groupKey: 'all' },
        },
      } as any,
      'panel-remote',
    )
    const bookmarkStore = useBookmarkStore()
    const wrapper = mount(BottomBar)
    await nextTick()

    expect(wrapper.findAll('.service-chip')).toHaveLength(1)
    expect(wrapper.text()).toContain('api')
    expect(wrapper.text()).not.toContain('remote-prod')

    await wrapper.find('.sync-label input[type="checkbox"]').setValue(true)
    await wrapper.find('.sync-record-btn').trigger('click')

    expect(bookmarkStore.syncRecording).toBe(true)
    expect([...bookmarkStore.syncPanelIds].sort()).toEqual(['panel-local', 'panel-remote'])
    expect((bookmarkStore.getBookmark('panel-local') as any)?.source).toEqual({
      type: 'local-service',
      projectId: 'proj-1',
      serviceId: 'svc-api',
    })
    expect((bookmarkStore.getBookmark('panel-remote') as any)?.source).toEqual({
      type: 'remote-log-source',
      logSourceId: 'remote-prod',
      groupKey: 'all',
    })
  })


  it('同步录制复制与导出使用一致的本地/远程来源分组格式', async () => {
    const bookmarkStore = useBookmarkStore()
    bookmarkStore.startSyncBookmark([
      {
        panelId: 'panel-local',
        serviceId: 'svc-api',
        source: { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-api' },
      },
      {
        panelId: 'panel-remote',
        serviceId: null,
        source: { type: 'remote-log-source', logSourceId: 'remote-prod', groupKey: 'all' },
      },
    ])
    const localLog = makeLog('svc-api', 'local copied', new Date().toISOString())
    const remoteLog = makeLog('remote-prod', 'remote copied', new Date().toISOString())
    bookmarkStore.appendToBookmark('panel-local', localLog)
    bookmarkStore.appendToBookmark('panel-remote', remoteLog)
    bookmarkStore.endSyncBookmark()

    const wrapper = mount(BottomBar)
    await nextTick()
    await wrapper.find('.sync-copy-btn').trigger('click')
    await wrapper.find('.sync-export-btn').trigger('click')
    await flushPromises()

    const copied = vi.mocked(navigator.clipboard.writeText).mock.calls[0][0]
    const exported = tauriMocks.writeTextFile.mock.calls[0][1]
    expect(exported).toBe(copied)
    expect(copied).toContain('=== svc-api ===')
    expect(copied).toContain('local copied')
    expect(copied).toContain('=== remote:remote-prod/all ===')
    expect(copied).toContain('remote copied')
  })

  it('录制中关闭参与的远程面板后停止同步录制保留该面板已录日志', async () => {
    const panelStore = usePanelStore()
    panelStore.setRoot(
      {
        type: 'split',
        id: 'split-1',
        axis: 'h',
        ratio: 0.5,
        first: {
          type: 'leaf',
          id: 'panel-local',
          source: { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-api' },
        },
        second: {
          type: 'leaf',
          id: 'panel-remote',
          source: { type: 'remote-log-source', logSourceId: 'remote-prod', groupKey: 'all' },
        },
      } as any,
      'panel-remote',
    )
    const wrapper = mount(BottomBar)
    const bookmarkStore = useBookmarkStore()
    await nextTick()

    await wrapper.find('.sync-label input[type="checkbox"]').setValue(true)
    await wrapper.find('.sync-record-btn').trigger('click')
    bookmarkStore.appendToBookmark(
      'panel-remote',
      makeLog('remote-prod', 'remote before close', new Date().toISOString()),
    )

    panelStore.removeLeaf('panel-remote')
    await nextTick()
    await wrapper.find('.sync-record-btn').trigger('click')

    const remoteBookmark = bookmarkStore.getBookmark('panel-remote')
    expect(remoteBookmark?.state).toBe('done')
    expect(remoteBookmark?.lockedLogs.map(log => log.message)).toEqual(['remote before close'])
    expect(bookmarkStore.formatSyncBookmarks()).toContain('=== remote:remote-prod/all ===')
  })
})
