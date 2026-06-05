/**
 * RuntimeWorkbenchHeader 组件测试。
 *
 * 职责：
 *   - 验证运行态工作区顶部状态来自 workspace/panel/bookmark stores
 *
 * 边界：
 *   - 不测试部署控制动作，header 只展示状态与布局入口
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import RuntimeWorkbenchHeader from '../RuntimeWorkbenchHeader.vue'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { usePanelStore, type PanelSplitNode } from '@/stores/panel'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'
import type { Project, Service } from '@/api/agent'

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
})
