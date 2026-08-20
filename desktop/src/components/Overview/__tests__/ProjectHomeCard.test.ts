/**
 * ProjectHomeCard 测试「开发环境归属」卡片的呈现与转移入口接线。
 *
 * 职责：
 *   - 验证徽标随 project.home_host_id 在「@ 本机」/「@ <host>」间切换
 *   - 验证 compact 模式仍展示转移操作，仅省略标题/描述/底部说明
 *   - 验证没有开发机主机时转移按钮置灰并展示「去设置页开启」提示
 *   - 验证唯一开发机时按钮文案直接带主机名，点击直接打开转移弹窗
 *   - 验证多台开发机时点击主按钮展开下拉，选择其一才打开转移弹窗（且带对的
 *     host_id 调用只读预检）
 *   - 验证归属在远端时按钮变为「迁回本机…」，点击打开的弹窗走 transferBack
 *     方向（不传 host_id）
 *
 * 边界：
 *   - 不访问真实后端；api.transferPreflight/startTransfer/transferStatus/
 *     transferBack 均为 mock，remoteStore.hosts 直接赋值，不经 api.listHosts
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ProjectHomeCard from '@/components/Overview/ProjectHomeCard.vue'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'
import type { Host, Project } from '@/api/agent'

const push = vi.fn()
vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      transferPreflight: vi.fn(),
      startTransfer: vi.fn(),
      transferStatus: vi.fn(),
      transferBack: vi.fn(),
    },
  }
})

function project(overrides: Partial<Project> = {}): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    services: [],
    ...overrides,
  }
}

function devHost(overrides: Partial<Host> = {}): Host {
  return { id: 'host-1', name: 'aliyun-1', tags: [], dev_machine_mode: true, ...overrides }
}

function mountCard(props: { project: Project; compact?: boolean }) {
  return mount(ProjectHomeCard, {
    props,
    global: { plugins: [installTestI18n()] },
  })
}

describe('ProjectHomeCard 徽标与 compact 收敛', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    push.mockClear()
    vi.clearAllMocks()
  })

  it('home_host_id 为空时展示「@ 本机」', () => {
    const wrapper = mountCard({ project: project() })

    expect(wrapper.find('[data-test="home-badge"]').text()).toBe('@ 本机')
  })

  it('home_host_id 非空时展示「@ <host名>」', () => {
    const wrapper = mountCard({ project: project({ home_host_id: 'host-9', home_host_name: 'aliyun-9' }) })

    expect(wrapper.find('[data-test="home-badge"]').text()).toBe('@ aliyun-9')
  })

  it('compact 模式仍展示转移操作，仅省略标题/描述/底部说明', () => {
    const wrapper = mountCard({ project: project(), compact: true })

    expect(wrapper.find('[data-test="home-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-title"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-desc"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-actions"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-transfer-btn"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-footer"]').exists()).toBe(false)
  })

  it('两种模式下操作区都是头部的兄弟节点，而非嵌在头部内', () => {
    // 这条钉的是「操作区只有一份」的**可观测形态**。注意 DOM 计数钉不住它：
    // 曾经的实现是 compact 与完整页各一份、用 v-if 互斥，任何时刻都只渲染
    // 一个，findAll 照样返回 1。区别只在结构——那份复制品挂在 .home-card-head
    // 里面，而现在两种模式共用同一个、始终是 head 的兄弟节点，位置差异交给
    // .home-card.compact 的两列栅格。所以断言父子关系，不断言数量。
    for (const compact of [true, false]) {
      const wrapper = mountCard({ project: project(), compact })
      const actions = wrapper.find('[data-test="home-actions"]')
      expect(actions.exists()).toBe(true)
      expect(actions.element.parentElement).toBe(wrapper.find('[data-test="project-home-card"]').element)
      expect(wrapper.find('.home-card-head [data-test="home-actions"]').exists()).toBe(false)
    }
  })

  it('compact 模式单台开发机时可直接打开转移弹窗', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({
      blockers: [],
      ready: [],
      target_dir: '~/workspace/demo',
      branch: 'main',
    })
    const remote = useRemoteStore()
    remote.hosts = [devHost()]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mountCard({ project: project(), compact: true })
    await new Promise(resolve => setTimeout(resolve))

    const btn = wrapper.find('[data-test="home-transfer-btn"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)
    expect(btn.text()).toContain('aliyun-1')
    await btn.trigger('click')

    expect(wrapper.find('[data-test="project-transfer-dialog"]').exists()).toBe(true)
    await vi.waitFor(() => expect(api.transferPreflight).toHaveBeenCalledWith('p1', 'host-1', undefined))
  })

  it('非 compact 模式渲染标题、描述、操作区和底部说明', () => {
    const wrapper = mountCard({ project: project() })

    expect(wrapper.find('[data-test="home-title"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-desc"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-actions"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="home-footer"]').text()).toContain('归属唯一')
  })
})

describe('ProjectHomeCard 无开发机主机', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    push.mockClear()
    vi.clearAllMocks()
  })

  it('转移按钮置灰并展示去设置页开启提示', async () => {
    const remote = useRemoteStore()
    remote.hosts = [{ id: 'host-1', name: 'plain-host', tags: [] }]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mountCard({ project: project() })
    await new Promise(resolve => setTimeout(resolve))

    const btn = wrapper.find('[data-test="home-transfer-btn"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    expect(wrapper.find('[data-test="home-transfer-hint"]').exists()).toBe(true)

    await wrapper.find('[data-test="home-goto-settings"]').trigger('click')
    expect(push).toHaveBeenCalledWith({ path: '/settings', query: { tab: 'hosts' } })
  })
})

describe('ProjectHomeCard 单台开发机', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('按钮文案带主机名，点击直接打开转移弹窗并按该主机预检', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({ blockers: [], ready: [], target_dir: '~/workspace/demo', branch: 'main' })
    const remote = useRemoteStore()
    remote.hosts = [devHost()]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mountCard({ project: project() })
    await new Promise(resolve => setTimeout(resolve))

    const btn = wrapper.find('[data-test="home-transfer-btn"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)
    expect(btn.text()).toContain('aliyun-1')

    await btn.trigger('click')

    expect(wrapper.find('[data-test="project-transfer-dialog"]').exists()).toBe(true)
    await vi.waitFor(() => expect(api.transferPreflight).toHaveBeenCalledWith('p1', 'host-1', undefined))
  })
})

describe('ProjectHomeCard 多台开发机', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('点击主按钮展开下拉，选择其一才打开弹窗并带对的 host_id 预检', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({ blockers: [], ready: [], target_dir: '~/workspace/demo', branch: 'main' })
    const remote = useRemoteStore()
    remote.hosts = [devHost({ id: 'host-1', name: 'aliyun-1' }), devHost({ id: 'host-2', name: 'aliyun-2' })]
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)

    const wrapper = mountCard({ project: project() })
    await new Promise(resolve => setTimeout(resolve))

    await wrapper.find('[data-test="home-transfer-btn"]').trigger('click')
    expect(wrapper.find('[data-test="home-dev-machine-menu"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="project-transfer-dialog"]').exists()).toBe(false)

    await wrapper.find('[data-test="home-dev-machine-item-host-2"]').trigger('click')

    expect(wrapper.find('[data-test="home-dev-machine-menu"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="project-transfer-dialog"]').exists()).toBe(true)
    await vi.waitFor(() => expect(api.transferPreflight).toHaveBeenCalledWith('p1', 'host-2', undefined))
  })
})

describe('ProjectHomeCard 归属在远端', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('按钮变为迁回本机，点击打开的弹窗走 transferBack 方向', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferBack).mockResolvedValue({ state: 'running', steps: [] })

    const wrapper = mountCard({ project: project({ home_host_id: 'host-9', home_host_name: 'aliyun-9' }) })
    await new Promise(resolve => setTimeout(resolve))

    const btn = wrapper.find('[data-test="home-transfer-back-btn"]')
    expect(btn.text()).toContain('迁移回本机')
    await btn.trigger('click')

    expect(wrapper.find('[data-test="project-transfer-dialog"]').exists()).toBe(true)
    expect(api.transferPreflight).not.toHaveBeenCalled()

    await wrapper.find('[data-test="transfer-execute"]').trigger('click')
    await vi.waitFor(() => expect(api.transferBack).toHaveBeenCalledWith('p1'))
  })
})
