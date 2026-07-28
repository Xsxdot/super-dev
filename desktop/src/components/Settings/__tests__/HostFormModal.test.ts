/**
 * HostFormModal 测试单主机身份表单。
 *
 * 职责：
 *   - 验证 Host identity-only 字段渲染
 *   - 验证入口地址元数据随表单提交
 *   - 验证「无指纹时保存触发采集并要求确认」的核心交互
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不测试 Agent 连接配置
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import HostFormModal from '@/components/Settings/HostFormModal.vue'
import type { Host } from '@/api/agent'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'

// mountForm 统一挂载 HostFormModal 并按需 spy store.scanHostKey，
// 复用既有测试文件的 Pinia + installTestI18n 约定（见 HostManagerTab.test.ts）。
function mountForm(options: { scanHostKey?: ReturnType<typeof vi.fn>; initial?: Partial<Host> | null } = {}) {
  setActivePinia(createPinia())
  const store = useRemoteStore()
  if (options.scanHostKey) {
    vi.spyOn(store, 'scanHostKey').mockImplementation(options.scanHostKey)
  }
  return mount(HostFormModal, {
    props: {
      visible: true,
      initial: (options.initial as Host | null | undefined) ?? null,
    },
    global: { plugins: [installTestI18n('zh-CN')] },
  })
}

describe('HostFormModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })


  it('uses shared settings modal and field classes', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('.settings-modal-backdrop').exists()).toBe(true)
    expect(wrapper.find('.settings-modal').exists()).toBe(true)
    expect(wrapper.findAll('.settings-field').length).toBeGreaterThan(0)
    expect(wrapper.find('[data-test="host-form-name"]').classes()).toContain('settings-input')
  })

  it('renders identity-only host fields', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-public-ip"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-private-ip"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-host"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-form-agent-port"]').exists()).toBe(false)
  })

  it('emits public and private IP fields', async () => {
    // 该 Host 尚无指纹，保存会先触发采集；这里 mock scanHostKey 并确认后再校验 payload。
    const scanHostKey = vi.fn().mockResolvedValue({ fingerprint: 'SHA256:abc123' })
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-name"]').setValue('edge')
    await wrapper.find('[data-test="host-form-public-ip"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="host-form-private-ip"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="host-form-scan-trust"]').trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual(expect.objectContaining({
      name: 'edge',
      public_ip: '203.0.113.10',
      private_ip: '10.0.0.10',
    }))
  })

  it('accepts a trusted external SSH host-key fingerprint entered manually', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true, initial: null },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="host-form-name"]').setValue('edge')
    // 手填指纹入口默认折叠，先展开再填值：一旦手填非空，needsScan 判定为 false，保存不再触发采集。
    await wrapper.find('[data-test="host-form-manual-fingerprint-toggle"]').trigger('click')
    await wrapper.find('[data-test="host-form-ssh-host-key-fingerprint"]').setValue('SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual(expect.objectContaining({
      ssh_host_key_fingerprint: 'SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A',
    }))
  })

  it('hydrates existing identity fields when editing', async () => {
    const wrapper = mount(HostFormModal, {
      props: {
        visible: true,
        initial: {
          id: 'host-1',
          name: 'edge',
          public_ip: '203.0.113.10',
          private_ip: '10.0.0.10',
          tags: ['prod'],
        },
      },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect((wrapper.find('[data-test="host-form-name"]').element as HTMLInputElement).value).toBe('edge')
    expect((wrapper.find('[data-test="host-form-public-ip"]').element as HTMLInputElement).value).toBe('203.0.113.10')
  })

  it('does not hydrate stored secrets and emits explicit clear intent', async () => {
    // ssh_host_key_fingerprint_configured: true 让 needsScan 为 false，保存直接提交，不触发采集。
    const wrapper = mount(HostFormModal, {
      props: {
        visible: true,
        initial: {
          id: 'host-1',
          name: 'edge',
          tags: [],
          ssh_credential_configured: true,
          ssh_password_configured: true,
          ssh_private_key_configured: true,
          ssh_host_key_fingerprint_configured: true,
        },
      },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect((wrapper.find('[data-test="host-form-ssh-password"]').element as HTMLInputElement).value).toBe('')
    expect((wrapper.find('[data-test="host-form-ssh-private-key"]').element as HTMLTextAreaElement).value).toBe('')
    await wrapper.find('[data-test="host-form-clear-ssh-password"]').setValue(true)
    await wrapper.find('[data-test="host-form-clear-ssh-private-key"]').setValue(true)
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual(expect.objectContaining({
      clear_ssh_password: true,
      clear_ssh_private_key: true,
    }))
  })

  it('does not emit replacement values together with explicit clear intent', async () => {
    const wrapper = mount(HostFormModal, {
      props: {
        visible: true,
        initial: {
          id: 'host-1',
          name: 'edge',
          tags: [],
          ssh_credential_configured: true,
          ssh_password_configured: true,
          ssh_private_key_configured: true,
          ssh_host_key_fingerprint_configured: true,
        },
      },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="host-form-ssh-password"]').setValue('replacement-password')
    await wrapper.find('[data-test="host-form-ssh-private-key"]').setValue('replacement-private-key')
    await wrapper.find('[data-test="host-form-clear-ssh-password"]').setValue(true)
    await wrapper.find('[data-test="host-form-clear-ssh-private-key"]').setValue(true)
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual(expect.objectContaining({
      ssh_password: '',
      ssh_private_key: '',
      clear_ssh_password: true,
      clear_ssh_private_key: true,
    }))
  })

  it('warns before an SSH target edit can invalidate an active tunnel', async () => {
    const wrapper = mount(HostFormModal, {
      props: {
        visible: true,
        initial: {
          id: 'host-1',
          name: 'edge',
          tags: [],
          ssh_host: 'ssh.example.com',
          ssh_port: 22,
          ssh_user: 'deploy',
        },
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="host-form-tunnel-invalidation"]').exists()).toBe(false)
    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('new-ssh.example.com')

    expect(wrapper.get('[data-test="host-form-tunnel-invalidation"]').text()).toContain('disconnect')
  })

  it('shows save and audit recovery errors inside the modal', () => {
    const wrapper = mount(HostFormModal, {
      props: {
        visible: true,
        error: 'Configuration was saved; retry to complete the audit.',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.get('[data-test="host-form-error"]').text()).toContain('retry to complete the audit')
  })

  // 主流程：无指纹时保存触发采集并弹确认卡片
  it('scans and shows the confirm card when the host has no fingerprint', async () => {
    const scanHostKey = vi.fn().mockResolvedValue({ fingerprint: 'SHA256:abc123' })
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-name"]').setValue('ali-01')
    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()

    expect(scanHostKey).toHaveBeenCalledWith({ ssh_host: '10.0.0.10', ssh_port: 22 })
    expect(wrapper.find('[data-test="host-form-scan-confirm"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-scan-fingerprint"]').text()).toContain('SHA256:abc123')
    expect(wrapper.emitted('submit')).toBeFalsy()
  })

  // 确认后才提交，且指纹进入 payload
  it('emits submit with the scanned fingerprint after the user trusts it', async () => {
    const scanHostKey = vi.fn().mockResolvedValue({ fingerprint: 'SHA256:abc123' })
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-name"]').setValue('ali-01')
    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="host-form-scan-trust"]').trigger('click')

    const submitted = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(submitted.ssh_host_key_fingerprint).toBe('SHA256:abc123')
  })

  // 取消确认不得提交
  it('does not submit when the user cancels the confirm card', async () => {
    const scanHostKey = vi.fn().mockResolvedValue({ fingerprint: 'SHA256:abc123' })
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="host-form-scan-cancel"]').trigger('click')

    expect(wrapper.emitted('submit')).toBeFalsy()
  })

  // 安全回归：确认卡片展示期间改动地址，指纹必须作废，不能把旧地址的指纹绑定到新地址
  it('invalidates the confirm card when the ssh_host is edited afterwards', async () => {
    const scanHostKey = vi.fn().mockResolvedValue({ fingerprint: 'SHA256:abc123' })
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="host-form-scan-confirm"]').exists()).toBe(true)

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.20')

    expect(wrapper.find('[data-test="host-form-scan-confirm"]').exists()).toBe(false)
    // trust 按钮已随卡片一起消失，无法再点击提交出旧指纹。
    expect(wrapper.find('[data-test="host-form-scan-trust"]').exists()).toBe(false)
    expect(wrapper.emitted('submit')).toBeFalsy()
  })

  // 同上，端口变化也必须作废确认卡片
  it('invalidates the confirm card when the ssh_port is edited afterwards', async () => {
    const scanHostKey = vi.fn().mockResolvedValue({ fingerprint: 'SHA256:abc123' })
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="host-form-scan-confirm"]').exists()).toBe(true)

    await wrapper.find('[data-test="host-form-ssh-port"]').setValue('2222')

    expect(wrapper.find('[data-test="host-form-scan-confirm"]').exists()).toBe(false)
    expect(wrapper.emitted('submit')).toBeFalsy()
  })

  // 安全回归：失败卡片展示期间改动地址，也必须作废（不能残留旧地址的错误状态）
  it('invalidates the failed card when the ssh_host is edited afterwards', async () => {
    const scanHostKey = vi.fn().mockRejectedValue(new Error('unreachable'))
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="host-form-scan-failed"]').exists()).toBe(true)

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.20')

    expect(wrapper.find('[data-test="host-form-scan-failed"]').exists()).toBe(false)
  })

  // 安全回归：飞行中的采集请求返回时，若地址已被改动，结果必须被丢弃，不能把
  // 旧地址的采集结果（无论成功或失败）套用到新地址上。
  it('discards an in-flight scan result if the address changed before it resolved', async () => {
    let resolveScan: (value: { fingerprint: string }) => void
    const scanHostKey = vi.fn().mockImplementation(() => new Promise(resolve => { resolveScan = resolve }))
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    expect(wrapper.find('[data-test="host-form-scanning"]').exists()).toBe(true)

    // 采集还在飞行中时改地址：watcher 立刻把 scanPhase 重置回 idle。
    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.20')
    expect(wrapper.find('[data-test="host-form-scanning"]').exists()).toBe(false)

    // 旧请求这时才返回旧地址的指纹，不应该把界面又推回 confirm。
    resolveScan!({ fingerprint: 'SHA256:stale-for-old-address' })
    await flushPromises()

    expect(wrapper.find('[data-test="host-form-scan-confirm"]').exists()).toBe(false)
    expect(wrapper.emitted('submit')).toBeFalsy()
  })

  // 采集失败展开手填
  it('reveals manual entry when the scan fails', async () => {
    const scanHostKey = vi.fn().mockRejectedValue(Object.assign(new Error('unreachable'), { code: 'ssh_host_unreachable' }))
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="host-form-scan-failed"]').exists()).toBe(true)
    await wrapper.find('[data-test="host-form-scan-manual"]').trigger('click')
    expect(wrapper.find('[data-test="host-form-ssh-host-key-fingerprint"]').isVisible()).toBe(true)
  })

  // 采集失败后可显式选择不带指纹保存
  it('allows saving without a fingerprint after a failed scan', async () => {
    const scanHostKey = vi.fn().mockRejectedValue(new Error('unreachable'))
    const wrapper = mountForm({ scanHostKey })

    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="host-form-save-without-fingerprint"]').trigger('click')

    const submitted = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(submitted.ssh_host_key_fingerprint).toBe('')
  })

  // 手填输入框默认折叠
  it('keeps the manual fingerprint input collapsed by default', () => {
    const wrapper = mountForm({})
    expect(wrapper.find('[data-test="host-form-ssh-host-key-fingerprint"]').exists()).toBe(false)
  })

  // 已有指纹的 Host 编辑保存不触发采集
  it('does not scan when editing a host that already has a fingerprint', async () => {
    const scanHostKey = vi.fn()
    const wrapper = mountForm({
      scanHostKey,
      initial: { id: 'h1', name: 'ali-01', ssh_host: '10.0.0.10', ssh_port: 22, ssh_user: 'root', tags: [], ssh_host_key_fingerprint_configured: true },
    })

    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()

    expect(scanHostKey).not.toHaveBeenCalled()
    expect(wrapper.emitted('submit')).toBeTruthy()
  })

  // 清除指纹开关已从 UI 移除
  it('no longer renders the clear-fingerprint checkbox', () => {
    const wrapper = mountForm({
      initial: { id: 'h1', name: 'ali-01', tags: [], ssh_host_key_fingerprint_configured: true },
    })
    expect(wrapper.find('[data-test="host-form-clear-ssh-host-key-fingerprint"]').exists()).toBe(false)
  })
})
