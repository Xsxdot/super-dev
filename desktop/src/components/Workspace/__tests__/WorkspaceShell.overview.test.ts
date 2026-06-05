/**
 * WorkspaceShell Project Overview 渲染测试。
 *
 * 职责：
 *   - 验证 overview workspace tab 渲染项目概览内容
 *
 * 边界：
 *   - ProjectOverviewPane 使用轻量 stub
 *   - 不渲染真实运行状态、流水线或 Ingress 子页面
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkspaceShell from '../WorkspaceShell.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/components/Overview/ProjectOverviewPane.vue', () => ({
  default: {
    props: ['project'],
    template: '<section data-test="overview-pane">{{ project.name }}</section>',
  },
}))

vi.mock('@/components/Workspace/RuntimeWorkbenchHeader.vue', () => ({
  default: { template: '<header data-test="runtime-header-stub" />' },
}))

vi.mock('@/components/Workspace/WorkspaceTabs.vue', () => ({
  default: { template: '<nav data-test="workspace-tabs-stub" />' },
}))

vi.mock('@/components/Panel/PanelLayout.vue', () => ({
  default: { template: '<section data-test="panel-layout-stub" />' },
}))

describe('WorkspaceShell overview tab', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('renders project overview as a workspace tab', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]
    useWorkspaceStore().openProjectOverview('proj-1')

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="overview-pane"]').text()).toContain('Demo')
  })

  it('renders runtime header for deployment tabs', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [{
        id: 'svc-1',
        project_id: 'proj-1',
        name: 'api',
        status: 'running',
        required: false,
        order: 1,
        deployments: [{ id: 'dep-1', env_name: 'demo', location: 'local', status: 'running' }],
      }],
      environments: [{ id: 'env-demo', name: 'demo', is_dev: true, order: 0 }],
    }]
    useWorkspaceStore().openDeployment('dep-1', 'api · demo')

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="runtime-header-stub"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="panel-layout-stub"]').exists()).toBe(true)
  })

  it('does not render runtime header for overview tabs', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]
    useWorkspaceStore().openProjectOverview('proj-1')

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="runtime-header-stub"]').exists()).toBe(false)
  })

  it('hides workspace tabs while runtime workspace is maximized', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [{
        id: 'svc-1',
        project_id: 'proj-1',
        name: 'api',
        status: 'running',
        required: false,
        order: 1,
        deployments: [{ id: 'dep-1', env_name: 'demo', location: 'local', status: 'running' }],
      }],
      environments: [{ id: 'env-demo', name: 'demo', is_dev: true, order: 0 }],
    }]
    const workspace = useWorkspaceStore()
    workspace.openDeployment('dep-1', 'api · demo')
    workspace.setRuntimeWorkspaceMaximized(true)

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="workspace-tabs-stub"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="runtime-header-stub"]').exists()).toBe(true)
  })
})
