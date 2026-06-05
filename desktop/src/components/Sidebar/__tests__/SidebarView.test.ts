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
})
