/**
 * BottomBar 组件测试
 *
 * 职责：
 *   - 验证底部栏面板服务默认选中
 *   - 验证同步录制可开始、停止并产出可复制/导出的同步书签
 *
 * 边界：
 *   - 不测试 Tauri 文件对话框真实行为
 *   - 不建立 WebSocket 连接，日志通过 deploymentLogStore 直接注入
 */
import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import BottomBar from '../BottomBar.vue'
import { api as agentApi } from '../../api/agent'
import { useAgentStore } from '../../stores/agent'
import { useBookmarkStore } from '../../stores/bookmark'
import { useDeploymentLogStore } from '../../stores/deploymentLog'
import { useOperationApprovalStore } from '../../stores/operationApproval'
import { usePanelStore } from '../../stores/panel'
import { toDisplayEntry } from '../../lib/logEngine'
import { installTestI18n } from '@/test-utils/i18n'
import type { LogEntry, Project, Service } from '../../api/agent'

const tauriMocks = vi.hoisted(() => ({
  save: vi.fn(),
  writeTextFile: vi.fn(),
  routerPush: vi.fn(),
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  save: tauriMocks.save,
}))

vi.mock('@tauri-apps/plugin-fs', () => ({
  writeTextFile: tauriMocks.writeTextFile,
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: tauriMocks.routerPush }),
}))

// service id 'svc-x' 对应 dev 环境的 deployment id 'dep-x'，面板按 deploymentId 订阅。
function makeService(id: string, name: string): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: 'running',
    required: false,
    order: 1,
    deployments: [
      { id: id.replace('svc-', 'dep-'), env_name: 'dev', location: 'local', status: 'running' },
    ],
  }
}

function makeProject(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    env_selected_service_ids: {},
    environments: [{ id: 'e-dev', name: 'dev', is_dev: true, order: 0 }],
  }
}


function makeLog(deploymentId: string, message: string, timestamp: string): LogEntry {
  return {
    id: 101,
    deployment_id: deploymentId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

async function mountBottomBarWithServices(locale: 'zh-CN' | 'en-US' = 'zh-CN') {
  const panelStore = usePanelStore()
  const agentStore = useAgentStore()
  const apiService = makeService('svc-api', 'api')
  const worker = makeService('svc-worker', 'worker')
  agentStore.projects = [makeProject([apiService, worker])]

  // 面板订阅键为 deploymentId（dep-api / dep-worker）。
  const apiDep = apiService.deployments![0].id
  const workerDep = worker.deployments![0].id
  const firstPanelId = panelStore.root.id
  panelStore.replaceScope(firstPanelId, apiDep, null)
  panelStore.splitLeaf(firstPanelId, 'h', workerDep, null, 'second')

  const wrapper = mount(BottomBar, {
    global: { plugins: [installTestI18n(locale)] },
  })
  await nextTick()
  return { wrapper, panelStore, apiService, worker, apiDep, workerDep }
}

describe('BottomBar', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers()
    tauriMocks.save.mockResolvedValue('/tmp/sync-export')
    vi.spyOn(agentApi, 'listOperationApprovals').mockResolvedValue([])
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
    const { wrapper, panelStore, apiDep } = await mountBottomBarWithServices()
    const bookmarkStore = useBookmarkStore()
    const deploymentLogStore = useDeploymentLogStore()
    const apiPanel = panelStore.allLeaves.find(leaf => leaf.serviceId === apiDep)!

    await wrapper.find('[data-test="sync-toggle"]').setValue(true)
    await wrapper.find('[data-test="sync-record"]').trigger('click')

    expect(bookmarkStore.syncPanelIds.has(apiPanel.id)).toBe(true)
    expect(bookmarkStore.getBookmark(apiPanel.id)?.serviceId).toBe(apiDep)
    expect(bookmarkStore.syncRecording).toBe(true)
    expect(wrapper.find('[data-test="sync-copy"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sync-export"]').exists()).toBe(false)

    const start = bookmarkStore.getBookmark(apiPanel.id)!.startTime!
    const ts = new Date(start.getTime() + 1000).toISOString()
    deploymentLogStore.sessions.set(apiDep, {
      refCount: 1,
      ws: null,
      logs: [toDisplayEntry(makeLog(apiDep, 'sync captured', ts))],
      hasMoreHistory: true,
      oldestLoadedId: null,
      loadingMoreHistory: false,
    })
    vi.advanceTimersByTime(5000)

    await wrapper.find('[data-test="sync-record"]').trigger('click')

    expect(bookmarkStore.syncRecording).toBe(false)
    expect(bookmarkStore.getBookmark(apiPanel.id)?.lockedLogs.map(l => l.message)).toEqual([
      'sync captured',
    ])
    expect(wrapper.find('[data-test="sync-copy"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="sync-export"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="sync-copy"]').text()).not.toContain('Copy')
    expect(wrapper.find('[data-test="sync-export"]').text()).not.toContain('Export')
  })

  it('英文 locale 下渲染底部栏控制文案', async () => {
    const { wrapper } = await mountBottomBarWithServices('en-US')

    expect(wrapper.text()).toContain('Open Deployments')
    expect(wrapper.text()).toContain('Sync Evidence Capture')
    expect(wrapper.text()).toContain('Restart')
    expect(wrapper.text()).toContain('Stop')
  })

  it('只渲染一个 agent 状态并提供审批入口', async () => {
    vi.mocked(agentApi.listOperationApprovals).mockResolvedValue([
      {
        id: 'opa-1',
        status: 'pending',
        requested_by: 'mcp',
        requester_label: 'Codex',
        plan: {
          id: 'op-1',
          kind: 'runtime.restart',
          target: {},
          risk_level: 'high',
          requires_approval: true,
          denied: false,
          fingerprint: 'fp-1',
        },
      },
    ] as any)
    const { wrapper } = await mountBottomBarWithServices('en-US')
    const approvalStore = useOperationApprovalStore()

    await flushPromises()

    expect(wrapper.findAll('[data-test="agent-status"]')).toHaveLength(1)
    expect(approvalStore.pendingCount).toBe(1)
    expect(wrapper.find('[data-test="approvals-entry"]').text()).toContain('1')

    await wrapper.find('[data-test="approvals-entry"]').trigger('click')

    expect(tauriMocks.routerPush).toHaveBeenCalledWith({ path: '/settings', query: { tab: 'approvals' } })
  })
})
