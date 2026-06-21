/**
 * BottomBar 组件测试
 *
 * 职责：
 *   - 验证底部栏面板服务默认选中
 *   - 验证证据入口可显示 pin 数并打开证据抽屉
 *
 * 边界：
 *   - 不测试 Tauri 文件对话框真实行为
 *   - 不建立 WebSocket 连接
 */
import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import BottomBar from '../BottomBar.vue'
import { api as agentApi } from '../../api/agent'
import { useAgentStore } from '../../stores/agent'
import { useLogEvidenceStore } from '../../stores/logEvidence'
import { useOperationApprovalStore } from '../../stores/operationApproval'
import { usePanelStore } from '../../stores/panel'
import { useDeploymentNodeSelectionStore } from '../../stores/deploymentNodeSelection'
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

function makeRemoteService(
  id = 'svc-api',
  name = 'api',
  deploymentId = 'dep-api',
  hostIds = ['h1', 'h2'],
): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: 'running',
    required: false,
    order: 1,
    deployments: [{
      id: deploymentId,
      env_name: 'prod',
      location: 'remote',
      status: 'running',
      host_ids: hostIds,
      logs: { type: 'file_tail', path: '/var/log/api.log' },
    }],
  }
}


function makeLog(deploymentId: string, message: string, timestamp: string): LogEntry {
  return {
    id: '101',
    deployment_id: deploymentId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

function makePendingApproval() {
  return {
    id: 'opa-1',
    status: 'pending',
    requested_by: 'mcp',
    requester_label: 'Codex',
    plan: {
      id: 'op-1',
      kind: 'runtime.restart',
      target: { deployment_id: 'dep-api' },
      target_summary: 'demo/prod/api',
      risk_level: 'high',
      requires_approval: true,
      denied: false,
      fingerprint: 'fp-1',
    },
  } as any
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



  it('证据入口显示 pin 数并打开悬浮抽屉', async () => {
    const { wrapper, panelStore, apiDep } = await mountBottomBarWithServices()
    const evidenceStore = useLogEvidenceStore()
    const apiPanel = panelStore.allLeaves.find(leaf => leaf.serviceId === apiDep)!

    evidenceStore.addPin({
      panelId: apiPanel.id,
      trackId: apiPanel.id,
      trackLabel: 'api · dev',
      sourceKey: apiDep,
      log: makeLog(apiDep, 'sync captured', '2026-06-20T10:00:00.000Z'),
    })
    await nextTick()

    expect(wrapper.find('[data-test="bottom-evidence-open"]').text()).toContain('1')
    expect(wrapper.find('[data-test="sync-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sync-record"]').exists()).toBe(false)

    await wrapper.find('[data-test="bottom-evidence-open"]').trigger('click')

    expect(evidenceStore.drawerOpen).toBe(true)
    expect(wrapper.find('[data-test="evidence-drawer"]').exists()).toBe(true)
  })

  it('渲染统一后的证据文案', async () => {
    const { wrapper: zhWrapper } = await mountBottomBarWithServices()

    expect(zhWrapper.text()).toContain('打开的部署')
    expect(zhWrapper.text()).toContain('证据')
    expect(zhWrapper.text()).not.toContain('同步录制')
    expect(zhWrapper.text()).not.toContain('日志录制')

    const { wrapper: enWrapper } = await mountBottomBarWithServices('en-US')

    expect(enWrapper.text()).toContain('Open Deployments')
    expect(enWrapper.text()).toContain('Evidence')
    expect(enWrapper.text()).not.toContain('Sync Recording')
    expect(enWrapper.text()).not.toContain('Log Recording')
    expect(enWrapper.text()).toContain('Restart')
    expect(enWrapper.text()).toContain('Stop')
  })

  it('只渲染一个 agent 状态并通过浮层提供审批入口', async () => {
    const pending = makePendingApproval()
    const approvalStore = useOperationApprovalStore()
    approvalStore.approvals = [pending]
    vi.mocked(agentApi.listOperationApprovals).mockResolvedValue([pending] as any)

    const { wrapper } = await mountBottomBarWithServices('en-US')

    await flushPromises()

    expect(wrapper.findAll('[data-test="agent-status"]')).toHaveLength(1)
    expect(approvalStore.pendingCount).toBe(1)
    expect(wrapper.find('[data-test="approvals-entry"]').text()).toContain('1')

    await wrapper.find('[data-test="approvals-entry"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="operation-approval-popover"]').exists()).toBe(true)
    expect(tauriMocks.routerPush).not.toHaveBeenCalled()

    await wrapper.find('[data-test="approval-popover-view-all"]').trigger('click')

    expect(tauriMocks.routerPush).toHaveBeenCalledWith({ path: '/settings', query: { tab: 'approvals' } })
  })

  it('不再渲染旧的同步录制控件', async () => {
    const { wrapper } = await mountBottomBarWithServices()

    expect(wrapper.find('[data-test="bottom-evidence"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="bottom-evidence-open"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="sync-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sync-record"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sync-copy"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sync-export"]').exists()).toBe(false)
  })

  it('renders bottom bar as grouped runtime action clusters', async () => {
    const { wrapper } = await mountBottomBarWithServices('en-US')

    expect(wrapper.find('[data-test="bottom-open-deployments"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="bottom-deployment-actions"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="bottom-evidence"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="bottom-runtime-status"]').exists()).toBe(true)
  })

  it('远端节点放在日志显示下拉多选中并支持取消勾选', async () => {
    vi.spyOn(agentApi, 'listHosts').mockResolvedValue([
      { id: 'h1', name: 'ali-01', private_ip: '10.0.0.1', tags: [] },
      { id: 'h2', name: 'jp', private_ip: '10.0.0.2', tags: [] },
    ])
    vi.spyOn(agentApi, 'getHostManagedDeploymentStatus').mockImplementation(async (hostId: string) => ({
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
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const nodeSelectionStore = useDeploymentNodeSelectionStore()
    agentStore.projects = [makeProject([makeRemoteService()])]
    panelStore.replaceScope(panelStore.root.id, 'dep-api', null)

    const wrapper = mount(BottomBar, {
      global: { plugins: [installTestI18n()] },
    })
    await flushPromises()

    expect(wrapper.find('[data-test="bottom-node-chip-list"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="bottom-log-display"]').text()).toContain('日志显示')
    expect(wrapper.find('[data-test="bottom-log-display-toggle"]').text()).toContain('节点 2/2')

    await wrapper.find('[data-test="bottom-log-display-toggle"]').trigger('click')
    expect(wrapper.findAll('[data-test="bottom-log-display-option"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="bottom-log-display-menu"]').text()).toContain('ali-01')

    await wrapper.findAll('[data-test="bottom-log-display-option"] input')[1].setValue(false)

    expect(nodeSelectionStore.selectedHostIds('dep-api')).toEqual(['h1'])
    expect(wrapper.find('[data-test="bottom-log-display-toggle"]').text()).toContain('节点 1/2')
  })

  it('一个日志显示下拉内按服务分组展示节点并独立勾选', async () => {
    vi.spyOn(agentApi, 'listHosts').mockResolvedValue([
      { id: 'h1', name: 'ali-01', private_ip: '10.0.0.1', tags: [] },
      { id: 'h2', name: 'jp', private_ip: '10.0.0.2', tags: [] },
      { id: 'h3', name: 'us-01', private_ip: '10.0.0.3', tags: [] },
    ])
    vi.spyOn(agentApi, 'getHostManagedDeploymentStatus').mockImplementation(async (hostId: string) => ({
      host_id: hostId,
      host_name: hostId,
      desired_deployment_count: 2,
      desired_collector_count: 2,
      tunnel_connected: true,
      remote: {
        deployment_count: 2,
        collector_count: 2,
        collectors: ['dep-api', 'dep-worker'].map(deploymentId => ({
          deployment_id: deploymentId,
          desired: true,
          running: true,
          status: 'running',
        })),
      },
    }))
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const nodeSelectionStore = useDeploymentNodeSelectionStore()
    const api = makeRemoteService('svc-api', 'api', 'dep-api', ['h1', 'h2'])
    const worker = makeRemoteService('svc-worker', 'worker', 'dep-worker', ['h2', 'h3'])
    agentStore.projects = [makeProject([api, worker])]
    const firstPanelId = panelStore.root.id
    panelStore.replaceScope(firstPanelId, 'dep-api', null)
    panelStore.splitLeaf(firstPanelId, 'h', 'dep-worker', null, 'second')

    const wrapper = mount(BottomBar, {
      global: { plugins: [installTestI18n()] },
    })
    await flushPromises()

    const toggles = wrapper.findAll('[data-test="bottom-log-display-toggle"]')
    expect(toggles).toHaveLength(1)
    expect(toggles[0].text()).toContain('服务 2')
    expect(toggles[0].text()).toContain('节点 4/4')

    await toggles[0].trigger('click')
    expect(wrapper.findAll('[data-test="bottom-log-display-service"]')).toHaveLength(2)
    const workerOptions = wrapper.findAll('[data-test="bottom-log-display-option"]')
    expect(workerOptions).toHaveLength(4)
    expect(wrapper.find('[data-test="bottom-log-display-menu"]').text()).toContain('api · prod')
    expect(wrapper.find('[data-test="bottom-log-display-menu"]').text()).toContain('worker · prod')
    expect(wrapper.find('[data-test="bottom-log-display-menu"]').text()).toContain('us-01')

    await workerOptions[3].find('input').setValue(false)

    expect(nodeSelectionStore.selectedHostIds('dep-api')).toEqual(['h1', 'h2'])
    expect(nodeSelectionStore.selectedHostIds('dep-worker')).toEqual(['h2'])
    expect(wrapper.find('[data-test="bottom-log-display-toggle"]').text()).toContain('节点 3/4')
  })
})
