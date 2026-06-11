/**
 * RuntimeWorkbenchHeader 组件测试。
 *
 * 职责：
 *   - 验证运行态工作区顶部状态来自 workspace/panel/bookmark stores
 *
 * 边界：
 *   - 不测试部署控制动作，header 只展示状态与布局入口
 */
import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuntimeWorkbenchHeader from '../RuntimeWorkbenchHeader.vue'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { usePanelStore, type PanelSplitNode } from '@/stores/panel'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'
import { AgentAPIError, api } from '@/api/agent'
import type { Project, Service } from '@/api/agent'
import { useOperationApprovalStore } from '@/stores/operationApproval'

const windowApiMock = vi.hoisted(() => ({
  startDragging: vi.fn(),
}))

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => windowApiMock,
}))

function makeService(): Service {
  return {
    id: 'svc-api',
    project_id: 'proj-1',
    name: 'sample-api',
    status: 'running',
    required: false,
    order: 1,
    version: 'v1.2.3',
    replicas: 2,
    deployments: [{ id: 'dep-api', env_name: 'demo', location: 'local', status: 'running' }],
  }
}

function makeProject(service: Service): Project {
  return {
    id: 'proj-1',
    name: 'SuperDev Sample',
    root_path: '/tmp/sample',
    services: [service],
    environments: [{ id: 'env-demo', name: 'demo', is_dev: true, order: 0 }],
  }
}

describe('RuntimeWorkbenchHeader', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    windowApiMock.startDragging.mockResolvedValue(undefined)
  })

  it('renders runtime context, deployment count, and panel count', () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    usePanelStore().replaceScope(usePanelStore().root.id, 'dep-api', null)

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.find('[data-test="runtime-workbench-header"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="runtime-title"]').text()).toContain('Runtime')
    expect(wrapper.find('[data-test="runtime-title"]').text()).toContain('demo')
    expect(wrapper.find('[data-test="runtime-deployments"]').text()).toContain('1 open deployment')
    expect(wrapper.find('[data-test="runtime-panel-count"]').text()).toContain('1 / 4 panels')
  })

  it('uses explicit Chinese labels for live tracking, open deployments, and evidence state', () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('zh-CN')] } })

    expect(wrapper.find('[data-test="runtime-live"]').text()).toContain('实时追踪中')
    expect(wrapper.find('[data-test="runtime-deployments"]').text()).toContain('已打开 1 个部署')
    expect(wrapper.find('[data-test="runtime-evidence"]').text()).toContain('录制未同步')
  })

  it('keeps a Tauri drag region in runtime chrome when the app topbar is hidden', () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.find('[data-test="runtime-drag-region"]').attributes('data-tauri-drag-region')).toBeDefined()
    expect(wrapper.find('[data-test="runtime-drag-spacer"]').attributes('data-tauri-drag-region')).toBeDefined()
  })

  it('starts native window dragging from runtime chrome drag areas', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })

    await wrapper.find('[data-test="runtime-drag-region"]').trigger('mousedown', { buttons: 1 })

    expect(windowApiMock.startDragging).toHaveBeenCalledTimes(1)
  })

  it('renders evidence state from bookmark store', () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    useBookmarkStore().setSyncEnabled(true)

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.find('[data-test="runtime-evidence"]').text()).toContain('Recording ready')
  })

  it('balances existing panel splits from the header action', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    const panelStore = usePanelStore()
    const [first] = panelStore.allLeaves
    panelStore.setRoot({
      type: 'split',
      id: 'split-root',
      axis: 'h',
      ratio: 0.8,
      first: { ...first, id: 'leaf-a', serviceId: 'dep-api', projectId: null, source: { type: 'deployment', deploymentId: 'dep-api' } },
      second: { ...first, id: 'leaf-b', serviceId: 'dep-api', projectId: null, source: { type: 'deployment', deploymentId: 'dep-api' } },
    })

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="layout-balance"]').trigger('click')

    expect((panelStore.root as PanelSplitNode).ratio).toBeCloseTo(0.5)
  })

  it('rearranges current panels into columns from the header action', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    const panelStore = usePanelStore()
    const [first] = panelStore.allLeaves
    panelStore.setRoot({
      type: 'split',
      id: 'split-root',
      axis: 'v',
      ratio: 0.5,
      first: { ...first, id: 'leaf-a', serviceId: 'dep-api', projectId: null, source: { type: 'deployment', deploymentId: 'dep-api' } },
      second: { ...first, id: 'leaf-b', serviceId: 'dep-api', projectId: null, source: { type: 'deployment', deploymentId: 'dep-api' } },
    })

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="layout-columns"]').trigger('click')

    expect((panelStore.root as PanelSplitNode).axis).toBe('h')
  })

  it('toggles runtime workspace maximized state from the header action', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    const workspace = useWorkspaceStore()
    workspace.openDeployment('dep-api', 'sample-api · demo')

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="layout-maximize"]').trigger('click')

    expect(workspace.isRuntimeWorkspaceMaximized).toBe(true)

    await wrapper.find('[data-test="layout-maximize"]').trigger('click')

    expect(workspace.isRuntimeWorkspaceMaximized).toBe(false)
  })

  it('opens a browser debug session for the active deployment', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    const openBrowserSession = vi.spyOn(api, 'openBrowserSession').mockResolvedValue({
      session_id: 'brs_1',
      deployment_id: 'dep-api',
      target_url: 'http://127.0.0.1:3000/',
      browser_id: 'arc',
      debug_port: 9222,
      browser_ws: 'ws://127.0.0.1:9222/devtools/browser/a',
      page_ws: 'ws://127.0.0.1:9222/devtools/page/p',
      devtools_url: 'http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/p',
    })

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="open-browser-debug"]').trigger('click')

    expect(openBrowserSession).toHaveBeenCalledWith({
      deployment_id: 'dep-api',
      open_devtools: true,
    }, undefined)
    expect(wrapper.text()).toContain('brs_1')
  })

  it('shows repair hint when debug browser is not configured', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    vi.spyOn(api, 'openBrowserSession').mockRejectedValue(new AgentAPIError('debug browser is not configured', 400, {
      code: 'debug_browser_not_configured',
      error: 'debug browser is not configured',
    }))

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="open-browser-debug"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="browser-debug-error"]').text()).toContain('Configure a debug browser')
  })

  it('shows target and DevTools link after opening browser debug session', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    vi.spyOn(api, 'openBrowserSession').mockResolvedValue({
      session_id: 'brs_1',
      deployment_id: 'dep-api',
      target_url: 'http://127.0.0.1:3000/',
      browser_id: 'arc',
      debug_port: 9222,
      browser_ws: 'ws://127.0.0.1:9222/devtools/browser/a',
      page_ws: 'ws://127.0.0.1:9222/devtools/page/p',
      devtools_url: 'http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/p',
      alive: true,
    })

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="open-browser-debug"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="browser-debug-target"]').text()).toContain('127.0.0.1:3000')
    expect(wrapper.find('[data-test="browser-debug-devtools"]').attributes('href')).toContain('/devtools/inspector.html')
  })

  it('clears stale browser debug session state when a later open fails', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    vi.spyOn(api, 'openBrowserSession')
      .mockResolvedValueOnce({
        session_id: 'brs_1',
        deployment_id: 'dep-api',
        target_url: 'http://127.0.0.1:3000/',
        browser_id: 'arc',
        debug_port: 9222,
        browser_ws: 'ws://127.0.0.1:9222/devtools/browser/a',
        page_ws: 'ws://127.0.0.1:9222/devtools/page/p',
        devtools_url: 'http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/p',
      })
      .mockRejectedValueOnce(new Error('browser unavailable'))

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="open-browser-debug"]').trigger('click')
    await wrapper.find('[data-test="open-browser-debug"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="browser-debug-session"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="browser-debug-error"]').text()).toContain('browser unavailable')
  })

  it('captures browser debug approval requests from the header action', async () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    const approval = {
      id: 'opa_browser',
      status: 'pending',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_browser',
        kind: 'browser_debug.open',
        target: { deployment_id: 'dep-api' },
        target_summary: 'demo/sample-api',
        risk_level: 'medium',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_browser',
      },
    } as any
    const error = new AgentAPIError('approval required', 403, {
      code: 'approval_required',
      error: 'approval required',
      approval,
      plan: approval.plan,
    })
    vi.spyOn(api, 'openBrowserSession').mockRejectedValue(error)
    const approvalStore = useOperationApprovalStore()
    const capture = vi.spyOn(approvalStore, 'captureApprovalRequired').mockResolvedValue(true)

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })
    await wrapper.find('[data-test="open-browser-debug"]').trigger('click')
    await flushPromises()

    expect(capture).toHaveBeenCalledWith(error)
    expect(wrapper.find('[data-test="browser-debug-error"]').text()).toContain('approval')
  })
})
