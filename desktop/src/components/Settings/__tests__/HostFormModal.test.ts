/**
 * HostFormModal 测试单主机表单 payload。
 *
 * 职责：
 *   - 验证入口地址元数据随表单提交
 *   - 验证 SSH 地址仍作为连接地址独立提交
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不调起真实 Tauri 文件对话框
 */
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import HostFormModal from '@/components/Settings/HostFormModal.vue'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      detectSshKeys: vi.fn().mockResolvedValue([]),
      testConnection: vi.fn().mockResolvedValue({ ok: true, message: 'ok', latency_ms: 1 }),
    },
  }
})

describe('HostFormModal', () => {
  it('emits public and private IP fields without changing SSH address', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true, initial: null },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="host-form-name"]').setValue('edge')
    await wrapper.find('[data-test="host-form-host"]').setValue('ssh.example.com')
    await wrapper.find('[data-test="host-form-user"]').setValue('deploy')
    await wrapper.find('[data-test="host-form-public-ip"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="host-form-private-ip"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual(expect.objectContaining({
      ssh_host: 'ssh.example.com',
      public_ip: '203.0.113.10',
      private_ip: '10.0.0.10',
    }))
  })
})
