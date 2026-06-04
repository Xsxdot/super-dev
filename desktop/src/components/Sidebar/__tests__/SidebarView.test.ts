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
