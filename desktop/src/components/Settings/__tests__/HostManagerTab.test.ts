/**
 * HostManagerTab 测试设置页 Host 身份管理能力。
 *
 * 职责：
 *   - 验证空态、新建入口和 Host SSH 连接信息 payload
 *   - 验证 Host 行不展示或操作 Agent 配置
 *
 * 边界：
 *   - 不访问真实 agent HTTP 或 WebSocket 接口
 *   - 不测试 Agent 配置 modal
 */
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { AgentAPIError, api, type Host } from '@/api/agent'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      createHost: vi.fn(),
      updateHost: vi.fn(),
      deleteHost: vi.fn(),
    },
  }
})

const mockedApi = api as unknown as Record<string, Mock>

function host(overrides: Partial<Host> = {}): Host {
  return {
    id: 'h1',
    name: 'host-test',
    public_ip: '203.0.113.10',
    private_ip: '10.0.0.10',
    ssh_host: '10.0.0.10',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_credential_configured: true,
    ssh_private_key_configured: true,
    ssh_host_key_fingerprint_configured: true,
    tags: [],
    ...overrides,
  }
}

async function mountHostManager() {
  const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
  await Promise.resolve()
  await Promise.resolve()
  return wrapper
}

describe('HostManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.listHosts.mockResolvedValue([])
    vi.stubGlobal('confirm', vi.fn().mockReturnValue(true))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('空态展示提示文案', async () => {
    const wrapper = await mountHostManager()

    expect(wrapper.text()).toContain('还没有主机')
  })

  it('点击新建主机打开 Host SSH 表单', async () => {
    const wrapper = await mountHostManager()

    await wrapper.find('[data-test="host-add"]').trigger('click')

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-ssh-host"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-ssh-private-key"]').exists()).toBe(true)
  })

  it('提交表单调用 store.createHost 且保存 SSH 私钥内容', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    const spy = vi.spyOn(store, 'createHost').mockResolvedValue(host())
    // 新建 Host 尚无指纹，保存会先触发采集卡片；这里 mock 采集结果并确认后再校验落库 payload。
    vi.spyOn(store, 'scanHostKey').mockResolvedValue({ fingerprint: 'SHA256:abc123' })

    await wrapper.find('[data-test="host-add"]').trigger('click')
    await wrapper.find('[data-test="host-form-name"]').setValue('host-test')
    await wrapper.find('[data-test="host-form-public-ip"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-ssh-user"]').setValue('root')
    await wrapper.find('[data-test="host-form-ssh-private-key"]').setValue('PRIVATE KEY CONTENT')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="host-form-scan-trust"]').trigger('click')

    expect(spy).toHaveBeenCalledWith(expect.objectContaining({
      name: 'host-test',
      public_ip: '203.0.113.10',
      ssh_host: '10.0.0.10',
      ssh_user: 'root',
      ssh_private_key: 'PRIVATE KEY CONTENT',
      ssh_host_key_fingerprint: 'SHA256:abc123',
    }))
    // 该用例走的是「粘贴私钥内容」路径，未选择导入文件，
    // 因此 payload 不应携带 ssh_key_path（导入路径与粘贴内容互斥，见 HostFormModal.buildPayload）。
    expect(spy.mock.calls[0][0].ssh_key_path ?? '').toBe('')
  })

  it('does not render Agent summary or Agent actions inside Host management', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [host({ tags: ['prod'] })]

    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="host-agent-summary"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-install-agent"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-refresh-agent-h1"]').exists()).toBe(false)
  })

  it('keeps a Host visible when Agent configuration blocks deletion', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [host()]
    mockedApi.deleteHost.mockRejectedValue(new AgentAPIError(
      'uninstall or detach the Agent before deleting the Host',
      409,
      { code: 'agent_configured' },
    ))
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-test="host-delete"]').trigger('click')

    await vi.waitFor(() => expect(wrapper.text()).toContain('uninstall or detach the Agent'))
    expect(store.hosts).toHaveLength(1)
    expect(wrapper.find('[data-test="host-row"]').exists()).toBe(true)
  })

  it('flags hosts that have no fingerprint', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'h1', name: 'ali-01', ssh_host: '10.0.0.10', ssh_host_key_fingerprint_configured: false }),
      host({ id: 'h2', name: 'jp', ssh_host: '10.0.0.11', ssh_host_key_fingerprint_configured: true }),
    ]
    await wrapper.vm.$nextTick()

    const warnings = wrapper.findAll('[data-test="host-fingerprint-missing"]')
    expect(warnings).toHaveLength(1)
  })

  it('does not flag a host with no SSH address configured at all', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'h1', name: 'no-ssh', ssh_host: undefined, ssh_host_key_fingerprint_configured: false }),
    ]
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="host-fingerprint-missing"]').exists()).toBe(false)
  })

  it('shows the rescan action only for hosts that already have a fingerprint', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'h1', name: 'ali-01', ssh_host_key_fingerprint_configured: false }),
      host({ id: 'h2', name: 'jp', ssh_host_key_fingerprint_configured: true }),
    ]
    await wrapper.vm.$nextTick()

    expect(wrapper.findAll('[data-test="host-rescan"]')).toHaveLength(1)
  })

  it('requires explicit confirmation before trusting a rescanned fingerprint', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'h2', name: 'jp', ssh_host: '10.0.0.11', ssh_port: 22, ssh_host_key_fingerprint_configured: true }),
    ]
    const scanHostKey = vi.spyOn(store, 'scanHostKey').mockResolvedValue({ fingerprint: 'SHA256:new999' })
    const updateHost = vi.spyOn(store, 'updateHost').mockResolvedValue(host())
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-test="host-rescan"]').trigger('click')
    await flushPromises()

    expect(scanHostKey).toHaveBeenCalledWith({ ssh_host: '10.0.0.11', ssh_port: 22 })
    expect(wrapper.find('[data-test="host-rescan-new-fingerprint"]').text()).toContain('SHA256:new999')
    expect(updateHost).not.toHaveBeenCalled()

    await wrapper.find('[data-test="host-rescan-confirm"]').trigger('click')
    await flushPromises()

    expect(updateHost).toHaveBeenCalledWith('h2', expect.objectContaining({
      ssh_host_key_fingerprint: 'SHA256:new999',
    }))
  })

  it('surfaces a scan failure in the rescan dialog and keeps confirm disabled', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'h2', name: 'jp', ssh_host: '10.0.0.11', ssh_port: 22, ssh_host_key_fingerprint_configured: true }),
    ]
    vi.spyOn(store, 'scanHostKey').mockRejectedValue(new AgentAPIError('scan failed', 502, { code: 'scan_failed' }))
    const updateHost = vi.spyOn(store, 'updateHost')
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-test="host-rescan"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="host-rescan-error"]').text()).toContain('scan failed')
    const confirmButton = wrapper.find('[data-test="host-rescan-confirm"]')
    expect((confirmButton.element as HTMLButtonElement).disabled).toBe(true)

    await confirmButton.trigger('click')
    await flushPromises()
    expect(updateHost).not.toHaveBeenCalled()
  })

  // 安全回归：A 主机的采集请求仍在飞行中时，用户关闭弹窗改采 B 主机；
  // A 的结果姗姗来迟时绝不能被套用到 B 的弹窗上，否则会把错误的指纹当作
  // 「用户已确认」写入 B——这正是 fail-closed 设计要防止的失败模式。
  it('discards a late-arriving scan result from a different host', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'hA', name: 'host-a', ssh_host: '10.0.0.10', ssh_port: 22, ssh_host_key_fingerprint_configured: true }),
      host({ id: 'hB', name: 'host-b', ssh_host: '10.0.0.20', ssh_port: 22, ssh_host_key_fingerprint_configured: true }),
    ]
    let resolveScanA: (value: { fingerprint: string }) => void
    const scanHostKey = vi.spyOn(store, 'scanHostKey').mockImplementationOnce(
      () => new Promise(resolve => { resolveScanA = resolve }),
    )
    const updateHost = vi.spyOn(store, 'updateHost').mockResolvedValue(host())
    await wrapper.vm.$nextTick()

    const rescanButtons = wrapper.findAll('[data-test="host-rescan"]')
    // 先在 A 上发起采集，此时请求挂起未返回。
    await rescanButtons[0].trigger('click')
    await flushPromises()

    // 关闭弹窗（点击遮罩层触发 @click.self），再在 B 上发起采集。
    await wrapper.find('.settings-modal-backdrop').trigger('click')
    scanHostKey.mockResolvedValueOnce({ fingerprint: 'SHA256:for-b' })
    await wrapper.findAll('[data-test="host-rescan"]')[1].trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="host-rescan-new-fingerprint"]').text()).toContain('SHA256:for-b')

    // A 的旧请求这时才返回，不应覆盖 B 正在展示的指纹。
    resolveScanA!({ fingerprint: 'SHA256:stale-for-a' })
    await flushPromises()

    expect(wrapper.find('[data-test="host-rescan-new-fingerprint"]').text()).toContain('SHA256:for-b')
    expect(wrapper.find('[data-test="host-rescan-new-fingerprint"]').text()).not.toContain('SHA256:stale-for-a')

    await wrapper.find('[data-test="host-rescan-confirm"]').trigger('click')
    await flushPromises()

    expect(updateHost).toHaveBeenCalledWith('hB', expect.objectContaining({
      ssh_host_key_fingerprint: 'SHA256:for-b',
    }))
    expect(updateHost).not.toHaveBeenCalledWith('hA', expect.anything())
    expect(updateHost).not.toHaveBeenCalledWith(expect.anything(), expect.objectContaining({
      ssh_host_key_fingerprint: 'SHA256:stale-for-a',
    }))
  })

  // 安全回归：A 主机采集失败的响应姗姗来迟时，也不能把错误状态套用到 B 的弹窗上
  // （否则用户会在 B 的弹窗里看到一个与 B 无关的错误信息，造成混淆）。
  it('discards a late-arriving scan failure from a different host', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'hA', name: 'host-a', ssh_host: '10.0.0.10', ssh_port: 22, ssh_host_key_fingerprint_configured: true }),
      host({ id: 'hB', name: 'host-b', ssh_host: '10.0.0.20', ssh_port: 22, ssh_host_key_fingerprint_configured: true }),
    ]
    let rejectScanA: (err: Error) => void
    const scanHostKey = vi.spyOn(store, 'scanHostKey').mockImplementationOnce(
      () => new Promise((_, reject) => { rejectScanA = reject }),
    )
    await wrapper.vm.$nextTick()

    const rescanButtons = wrapper.findAll('[data-test="host-rescan"]')
    await rescanButtons[0].trigger('click')
    await flushPromises()

    await wrapper.find('.settings-modal-backdrop').trigger('click')
    scanHostKey.mockResolvedValueOnce({ fingerprint: 'SHA256:for-b' })
    await wrapper.findAll('[data-test="host-rescan"]')[1].trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="host-rescan-new-fingerprint"]').text()).toContain('SHA256:for-b')

    rejectScanA!(new Error('unreachable'))
    await flushPromises()

    expect(wrapper.find('[data-test="host-rescan-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-rescan-new-fingerprint"]').text()).toContain('SHA256:for-b')
  })

  // 角色徽标（Task 12）：只有 dev_machine_mode 主机在列表行里展示「开发机」徽标。
  it('renders the dev machine badge only for hosts with dev_machine_mode', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [
      host({ id: 'h1', name: 'dev-box', dev_machine_mode: true }),
      host({ id: 'h2', name: 'plain-host', dev_machine_mode: false }),
    ]
    await wrapper.vm.$nextTick()

    const badges = wrapper.findAll('[data-test="host-dev-machine-badge"]')
    expect(badges).toHaveLength(1)
    expect(badges[0].text()).toBe('开发机')
  })
})
