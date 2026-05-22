/**
 * RemoteListenSection 测试 Sidebar 远程监听入口。
 *
 * 职责：
 *   - 验证主机管理齿轮跳转
 *   - 验证空态和新建监听任务入口
 *
 * 边界：
 *   - 不渲染真实日志面板
 *   - 不访问真实 agent HTTP 接口
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import RemoteListenSection from '@/components/Sidebar/RemoteListenSection.vue'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      listLogSources: vi.fn().mockResolvedValue([]),
      listTunnels: vi.fn().mockResolvedValue([]),
      createLogSource: vi.fn(),
      deleteLogSource: vi.fn(),
    },
  }
})

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/settings', component: { template: '<div />' } },
    ],
  })
}

describe('RemoteListenSection', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('齿轮按钮跳到 /settings?tab=hosts', async () => {
    const router = makeRouter()
    const wrapper = mount(RemoteListenSection, { global: { plugins: [router] } })
    await router.isReady()

    await wrapper.find('[data-test="remote-gear"]').trigger('click')
    await new Promise(resolve => setTimeout(resolve))

    expect(router.currentRoute.value.path).toBe('/settings')
    expect(router.currentRoute.value.query.tab).toBe('hosts')
  })

  it('空态展示提示', async () => {
    const router = makeRouter()
    const wrapper = mount(RemoteListenSection, { global: { plugins: [router] } })
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).toContain('还没有监听任务')
  })

  it('点击新建监听任务打开表单', async () => {
    const router = makeRouter()
    const wrapper = mount(RemoteListenSection, { global: { plugins: [router] } })
    await new Promise(resolve => setTimeout(resolve))

    await wrapper.find('[data-test="remote-add-logsource"]').trigger('click')

    expect(wrapper.find('[data-test="logsource-form-name"]').exists()).toBe(true)
  })
})
