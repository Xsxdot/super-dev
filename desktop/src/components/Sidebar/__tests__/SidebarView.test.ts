/**
 * SidebarView 测试主界面侧边栏入口。
 *
 * 职责：
 *   - 验证主界面添加项目入口复用项目配置初始化流程
 *
 * 边界：
 *   - 不打开真实目录选择器
 *   - 不渲染完整项目配置编辑器
 */
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { open, ask } from '@tauri-apps/plugin-dialog'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import { api, type Project } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { useGettingStartedStore } from '@/stores/gettingStarted'
import { useSettingsStore } from '@/stores/settings'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
  ask: vi.fn(),
  message: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@/components/Settings/ProjectConfigEditor.vue', () => ({
  default: {
    props: ['project', 'isNew'],
    template: '<section data-test="config-editor">{{ project.services[0]?.name }} · {{ isNew }}</section>',
  },
}))

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      getVscodeLaunch: vi.fn(),
      listOperationAudit: vi.fn().mockResolvedValue({ events: [], count: 0 }),
    },
  }
})

function createdProject(): Project {
  return {
    id: 'proj-1',
    name: 'Demo',
    root_path: '/tmp/demo',
    services: [],
    environments: [],
  }
}

function projectWithService(id: string, name: string, serviceName: string, envName = 'dev', isDev = true): Project {
  return {
    id,
    name,
    root_path: `/tmp/${id}`,
    services: [{
      id: `svc-${serviceName}`,
      project_id: id,
      name: serviceName,
      status: 'running',
      required: false,
      order: 1,
      deployments: [{ id: `dep-${serviceName}`, env_name: envName, location: 'local', status: 'running' }],
    }],
    environments: [{ id: `env-${id}`, name: envName, is_dev: isDev, order: 1 }],
  }
}

describe('SidebarView', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('主界面添加项目后进入和设置页一致的配置初始化流程', async () => {
    const agent = useAgentStore()
    vi.spyOn(agent, 'addProject').mockResolvedValue(createdProject())
    vi.mocked(api.getVscodeLaunch).mockResolvedValue([{
      name: 'web',
      command: 'pnpm dev',
      work_dir: '/tmp/demo',
      env: { PORT: '3000' },
    }])
    vi.mocked(open).mockResolvedValue('/tmp/demo')
    vi.mocked(ask).mockResolvedValue(true)

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="sidebar-add-project"]').trigger('click')
    await flushPromises()

    expect(agent.addProject).toHaveBeenCalledWith('/tmp/demo')
    expect(api.getVscodeLaunch).toHaveBeenCalledWith('proj-1')
    expect(wrapper.find('[data-test="config-editor"]').text()).toContain('web · true')
  })

  it('只渲染运行态入口，不渲染项目模块导航', () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="sidebar-service-search"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="sidebar-settings"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="sidebar-add-project"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Pipelines')
    expect(wrapper.text()).not.toContain('Ingress')
    expect(wrapper.text()).not.toContain('Evidence')
  })

  it('侧边栏以项目选择器展示当前项目，切换后只显示该项目服务', async () => {
    const agent = useAgentStore()
    agent.projects = [
      projectWithService('proj-1', 'SuperDev Sample', 'sample-api'),
      projectWithService('proj-2', 'TK', 'server'),
    ]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="project-selector"]').text()).toContain('SuperDev Sample')
    expect(wrapper.text()).toContain('sample-api')
    expect(wrapper.text()).not.toContain('server')

    await wrapper.find('[data-test="project-selector"]').trigger('click')
    await wrapper.find('[data-test="project-option-proj-2"]').trigger('click')

    expect(wrapper.find('[data-test="project-selector"]').text()).toContain('TK')
    expect(wrapper.text()).toContain('server')
    expect(wrapper.text()).not.toContain('sample-api')
  })

  it('⌘K 聚焦服务搜索框', async () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]

    const wrapper = mount(SidebarView, {
      attachTo: document.body,
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="sidebar-search-shortcut"]').text()).toBe('⌘K')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))
    await wrapper.vm.$nextTick()

    expect(document.activeElement).toBe(wrapper.find('[data-test="sidebar-service-search"]').element)
    wrapper.unmount()
  })

  it('项目选择菜单点击外部后关闭', async () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]

    const wrapper = mount(SidebarView, {
      attachTo: document.body,
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="project-selector"]').trigger('click')
    expect(wrapper.find('[data-test="project-menu"]').exists()).toBe(true)

    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="project-menu"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('项目选择菜单不包含项目概览入口', async () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="project-selector"]').trigger('click')

    expect(wrapper.find('[data-test="project-overview-menu"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="project-menu"]').text()).not.toContain('Project overview')
  })

  it('项目概览横栏位于拖拽分栏提示之前', () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('en-US')] },
    })
    const shell = wrapper.find('[data-test="sidebar-project-shell"]').element
    const overview = wrapper.find('[data-test="project-overview"]').element
    const dropHint = wrapper.find('[data-test="sidebar-drop-hint"]').element

    expect(shell.contains(overview)).toBe(true)
    expect(shell.contains(dropHint)).toBe(true)
    expect(overview.compareDocumentPosition(dropHint) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('首个非 dev 环境默认展开，服务行可直接拖拽', () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api', 'demo', false)]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="env-group-rows"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="env-service-row"]').text()).toContain('sample-api')
  })

  it('搜索服务时只保留匹配的 service 行', async () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [
        {
          id: 'svc-api',
          project_id: 'proj-1',
          name: 'sample-api',
          status: 'running',
          required: false,
          order: 1,
          deployments: [{ id: 'dep-api', env_name: 'dev', location: 'local', status: 'running' }],
        },
        {
          id: 'svc-worker',
          project_id: 'proj-1',
          name: 'worker',
          status: 'running',
          required: false,
          order: 2,
          deployments: [{ id: 'dep-worker', env_name: 'dev', location: 'local', status: 'running' }],
        },
      ],
      environments: [{ id: 'env-dev', name: 'dev', is_dev: true, order: 1 }],
    }]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n()] },
    })
    await wrapper.find('[data-test="sidebar-service-search"]').setValue('worker')

    expect(wrapper.text()).toContain('worker')
    expect(wrapper.text()).not.toContain('sample-api')
  })

  it('项目概览按钮打开 workspace overview tab', async () => {
    const agent = useAgentStore()
    agent.projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]
    const workspace = useWorkspaceStore()

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n()] },
    })
    await wrapper.find('[data-test="project-overview"]').trigger('click')

    const active = workspace.activeTab
    expect(active?.type).toBe('overview')
    if (active?.type !== 'overview') throw new Error('expected overview tab')
    expect(active.projectId).toBe('proj-1')
  })

  it('节点中心按钮打开全局 nodes workspace tab', async () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]
    const workspace = useWorkspaceStore()

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await wrapper.find('[data-test="sidebar-node-center"]').trigger('click')

    expect(workspace.activeTab?.type).toBe('nodes')
    expect(workspace.tabs.filter(tab => tab.type === 'nodes')).toHaveLength(1)
  })

  it('节点中心入口位于底部工具区并排在设置上方', () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const tools = wrapper.find('.sidebar-tools')
    expect(tools.exists()).toBe(true)
    expect(tools.find('[data-test="sidebar-node-center"]').exists()).toBe(true)
    expect(tools.find('[data-test="sidebar-settings"]').exists()).toBe(true)
    expect(tools.text().indexOf('节点中心')).toBeLessThan(tools.text().indexOf('设置'))
    expect(wrapper.find('.sidebar-scroll [data-test="sidebar-node-center"]').exists()).toBe(false)
  })

  it('节点中心和设置入口使用统一的底部工具按钮结构', () => {
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-1', 'Demo', 'sample-api')]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const tools = wrapper.find('.sidebar-tools')
    const buttons = tools.findAll('.sidebar-tool-button')

    expect(buttons).toHaveLength(2)
    expect(buttons[0].attributes('data-test')).toBe('sidebar-node-center')
    expect(buttons[1].attributes('data-test')).toBe('sidebar-settings')
    expect(buttons[0].find('.sidebar-tool-icon').exists()).toBe(true)
    expect(buttons[0].find('.sidebar-tool-main').text()).toBe('节点中心')
    expect(buttons[0].find('.sidebar-tool-hint').text()).toBe('所有远端节点')
    expect(buttons[1].find('.sidebar-tool-icon').exists()).toBe(true)
    expect(buttons[1].find('.sidebar-tool-main').text()).toBe('设置')
    expect(buttons[1].find('.sidebar-tool-hint').text()).toBe('偏好与管理')
  })

  it('onboarding 完成且有非 sample 本地部署时显示起步入口并标记 step2 完成', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = {
      ...settings.agentSettings,
      onboarding_completed: true,
      sample_seeded: true,
    }
    const agent = useAgentStore()
    agent.projects = [projectWithService('proj-local', 'My App', 'api')]

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flushPromises()

    const entry = wrapper.find('[data-test="getting-started-entry"]')
    expect(entry.exists()).toBe(true)
    expect(entry.text()).toContain('起步 2/5')

    await entry.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="getting-started-popover"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="step-step2"]').classes()).toContain('is-done')
    expect(useGettingStartedStore().completedSteps.sort()).toEqual(['step0', 'step2'])
  })

  it('首次挂载时主线前三步已完成的老用户自动关闭起步入口', async () => {
    vi.mocked(api.listOperationAudit).mockResolvedValueOnce({
      events: [{
        id: 'aud-1',
        kind: 'runtime.restart',
        action: 'approved',
        plan: { id: 'plan-1', kind: 'runtime.restart', target: {}, risk_level: 'low', requires_approval: true, denied: false, fingerprint: 'fp-1' },
        summary: 'operation approval approved',
      }],
      count: 1,
    })
    const settings = useSettingsStore()
    settings.agentSettings = {
      ...settings.agentSettings,
      onboarding_completed: true,
      sample_seeded: true,
    }
    const agent = useAgentStore()
    agent.projects = [
      projectWithService('sample', 'SuperDev Sample', 'sample-api'),
      projectWithService('proj-local', 'My App', 'api'),
    ]
    agent.projects[0].root_path = '/tmp/superdev-sample'

    const wrapper = mount(SidebarView, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flushPromises()

    expect(useGettingStartedStore().dismissed).toBe(true)
    expect(wrapper.find('[data-test="getting-started-entry"]').exists()).toBe(false)
  })
})
