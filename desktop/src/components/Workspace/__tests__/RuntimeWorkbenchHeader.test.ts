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
import { usePanelStore } from '@/stores/panel'
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
    expect(wrapper.find('[data-test="runtime-deployments"]').text()).toContain('1 deployment')
    expect(wrapper.find('[data-test="runtime-panel-count"]').text()).toContain('1 / 4 panels')
  })

  it('renders evidence state from bookmark store', () => {
    const service = makeService()
    useAgentStore().projects = [makeProject(service)]
    useWorkspaceStore().openDeployment('dep-api', 'sample-api · demo')
    useBookmarkStore().setSyncEnabled(true)

    const wrapper = mount(RuntimeWorkbenchHeader, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.find('[data-test="runtime-evidence"]').text()).toContain('Evidence sync ready')
  })
})
