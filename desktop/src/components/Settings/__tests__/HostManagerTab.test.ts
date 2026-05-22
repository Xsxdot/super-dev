/**
 * HostManagerTab 测试设置页主机管理能力。
 *
 * 职责：
 *   - 验证空态、新建入口与 SSH config 导入口
 *   - 验证 Host 表单提交会走 remote store action
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不调起真实 Tauri 文件对话框
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { useRemoteStore } from '@/stores/remote'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      listTunnels: vi.fn().mockResolvedValue([]),
      createHost: vi.fn(),
      updateHost: vi.fn(),
      deleteHost: vi.fn(),
      listSshConfigHosts: vi.fn().mockResolvedValue([]),
    },
  }
})

describe('HostManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('空态展示提示文案', async () => {
    const wrapper = mount(HostManagerTab)
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).toContain('还没有主机')
  })

  it('点击新建主机打开表单', async () => {
    const wrapper = mount(HostManagerTab)

    await wrapper.find('[data-test="host-add"]').trigger('click')

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
  })

  it('点击从 SSH config 导入打开导入对话框', async () => {
    const wrapper = mount(HostManagerTab)

    await wrapper.find('[data-test="host-import"]').trigger('click')

    expect(wrapper.text()).toContain('从 SSH config 导入')
  })

  it('提交表单调用 store.createHost', async () => {
    const wrapper = mount(HostManagerTab)
    const store = useRemoteStore()
    const spy = vi.spyOn(store, 'createHost').mockResolvedValue({
      id: 'h1',
      name: 'host-test',
      ssh_host: '1.1.1.1',
      ssh_port: 22,
      ssh_user: 'root',
      remote_agent_port: 57017,
      tags: [],
      created_at: '',
      updated_at: '',
    })

    await wrapper.find('[data-test="host-add"]').trigger('click')
    await wrapper.find('[data-test="host-form-name"]').setValue('host-test')
    await wrapper.find('[data-test="host-form-host"]').setValue('1.1.1.1')
    await wrapper.find('[data-test="host-form-user"]').setValue('root')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(spy).toHaveBeenCalled()
    expect(spy.mock.calls[0][0]).toMatchObject({
      name: 'host-test',
      ssh_host: '1.1.1.1',
      ssh_user: 'root',
    })
  })
})
