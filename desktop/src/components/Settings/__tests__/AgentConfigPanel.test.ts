/**
 * AgentConfigPanel unified lifecycle panel tests.
 *
 * 职责：
 *   - 验证四步 Agent 配置面板替代旧的三个独立弹窗
 *   - 验证监听/TLS 是安装参数的唯一编辑来源
 *   - 验证连接链编辑与探测流程仍通过 agents store
 *
 * 边界：
 *   - 不访问真实 agent HTTP API
 *   - 不测试 AgentManagerTab 行布局
 */
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentConfigPanel from '../AgentConfigPanel.vue'
import { useAgentsStore } from '@/stores/agents'
import { installTestI18n } from '@/test-utils/i18n'
import { AgentAPIError } from '@/api/agent'
import type { AgentDTO, Host, NodeStatus } from '@/api/agent'

// existingAgentDetectedError 构造安装守卫 409 响应对应的结构化错误，
// 供纳管相关用例复用（避免每个用例重复拼 payload）。
function existingAgentDetectedError(version = '1.4.0'): AgentAPIError {
  return new AgentAPIError('existing agent detected', 409, { code: 'existing_agent_detected', version })
}

function agent(overrides: Partial<AgentDTO> = {}): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: [],
    transport: {
      chain: [
        { type: 'direct', direct: { address: '100.64.0.8:57017' } },
        { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
      ],
    },
    config: { listen_address: '127.0.0.1', listen_port: 57017 },
    runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    security: { token_configured: false, provision_state: 'pending-bootstrap', tls: { mode: 'auto', server_name: 'ali-01' } },
    ...overrides,
  }
}

function node(): NodeStatus {
  return {
    host_id: 'h1',
    name: 'ali-01',
    reachable: true,
    agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    deployments: [],
    route: {
      selected_index: 1,
      selected_type: 'tunnel',
      degraded: true,
      last_results: [
        { index: 0, transport_type: 'direct', status: 'unreachable', reachable: false, error: 'connection refused', checked_at: '2026-06-07T10:00:00Z' },
        { index: 1, transport_type: 'tunnel', status: 'reachable', reachable: true, latency_ms: 8, checked_at: '2026-06-07T10:00:01Z' },
      ],
    },
    updated_at: '2026-06-07T10:00:00Z',
  }
}

const hosts: Host[] = [
  {
    id: 'h1',
    name: 'ali-01',
    public_ip: '203.0.113.10',
    private_ip: '10.0.0.8',
    ssh_host: '10.0.0.8',
    ssh_user: 'root',
    ssh_credential_configured: true,
    ssh_private_key_configured: true,
    ssh_host_key_fingerprint_configured: true,
    tags: ['prod'],
  },
]

const versionedReleaseURL = /^https:\/\/github\.com\/Xsxdot\/super-dev\/releases\/download\/v\d+\.\d+\.\d+$/

beforeEach(() => {
  vi.useRealTimers()
  setActivePinia(createPinia())
  vi.restoreAllMocks()
})

describe('AgentConfigPanel', () => {
  it('creates a new Agent without user-editable bind address and keeps tunnel port sourced from Listener & TLS', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'createAgent').mockResolvedValue(agent({
      transport: { chain: [{ type: 'tunnel', tunnel: { remote_agent_port: 57019 } }] },
      config: { listen_port: 57019 },
    }))
    vi.spyOn(store, 'generateInstallCommand').mockResolvedValue({ command: 'curl install', expires_at: '2026-06-07T10:30:00Z', token_id: 'tok_1' })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, mode: 'create', hosts, initialTab: 'security' },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.findAll('.panel-tab').map(tab => tab.attributes('data-test'))).toEqual([
      'agent-panel-tab-security',
      'agent-panel-tab-transport',
      'agent-panel-tab-install',
      'agent-panel-tab-probe',
    ])
    expect(wrapper.find('[data-test="agent-create-host"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-panel-tab-security"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-listen-address"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-public-ip-tls-hint"]').text()).toContain('公网 IP')
    await wrapper.find('[data-test="agent-listen-port"]').setValue(57019)
    await wrapper.find('[data-test="agent-security-save"]').trigger('click')

    expect(store.createAgent).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="agent-panel-tab-transport"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-create-before-transport"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tunnel-remote-agent-port-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tunnel-loopback-note-0"]').text()).toContain('57019')

    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')

    expect(store.createAgent).toHaveBeenCalledWith({
      host_id: 'h1',
      transport: { chain: [{ type: 'tunnel', tunnel: { remote_agent_port: 57019 } }] },
      config: { listen_port: 57019 },
      security: {
        token_configured: false,
        provision_state: 'pending-bootstrap',
        tls: { mode: 'auto' },
      },
    })
    expect(wrapper.emitted('created')?.[0]?.[0]).toMatchObject({ host_id: 'h1' })
    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-create-before-install"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-install-generate"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-install-bind-preview"]').text()).toContain('127.0.0.1:57019')

    await wrapper.find('[data-test="agent-install-generate"]').trigger('click')

    expect(store.generateInstallCommand).toHaveBeenCalledWith('h1', {
      method: 'generated_command',
      controller_url: 'http://127.0.0.1:57017',
      release_base_url: expect.stringMatching(versionedReleaseURL),
      remote_agent_port: 57019,
      transport_type: 'tunnel',
      token_ttl_minutes: 30,
    })
  })

  it('syncs the default create-mode tunnel port when the connection-chain tab is opened directly', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, mode: 'create', hosts, initialTab: 'security' },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="agent-listen-port"]').setValue(57021)
    await wrapper.find('[data-test="agent-panel-tab-transport"]').trigger('click')

    expect(wrapper.find('[data-test="tunnel-loopback-note-0"]').text()).toContain('57021')
  })

  it('opens on the requested default tab', () => {
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'install' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-install-generate"]').exists()).toBe(true)
  })

  it('saves listen port and manual TLS config from the security tab without persisting listen_address', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentConfig').mockResolvedValue(agent({ config: { listen_port: 57017 } }))
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), host: hosts[0], initialTab: 'security' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-listen-address"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-public-ip-tls-hint"]').text()).toContain('public IP')
    await wrapper.find('[data-test="agent-tls-mode-manual"]').setValue(true)
    await wrapper.find('[data-test="agent-manual-advanced-toggle"]').trigger('click')
    await wrapper.find('[data-test="agent-server-name"]').setValue('agent.internal')
    await wrapper.find('[data-test="agent-ca-cert"]').setValue('PEM')
    await wrapper.find('[data-test="agent-security-save"]').trigger('click')

    expect(store.updateAgentConfig).toHaveBeenCalledWith('h1', {
      config: { listen_port: 57017 },
      security: {
        token_configured: false,
        provision_state: 'pending-bootstrap',
        tls: { mode: 'manual', server_name: 'agent.internal', ca_cert: 'PEM' },
      },
    })
  })

  it('hides manual certificate fields until manual TLS advanced is opened', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'security' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-server-name"]').exists()).toBe(false)
    await wrapper.find('[data-test="agent-tls-mode-manual"]').setValue(true)
    expect(wrapper.find('[data-test="agent-server-name"]').exists()).toBe(false)
    await wrapper.find('[data-test="agent-manual-advanced-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="agent-server-name"]').exists()).toBe(true)
  })

  it('generates install command from derived bind preview without sending bind_address from the browser', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'generateInstallCommand').mockResolvedValue({ command: 'curl install', expires_at: '2026-06-07T10:30:00Z', token_id: 'tok_1' })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), host: hosts[0], initialTab: 'install' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-install-bind-address-input"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-install-bind-preview"]').text()).toContain('0.0.0.0:57017')
    expect(wrapper.find('[data-test="agent-bind-reason"]').text()).toContain('direct')
    await wrapper.find('[data-test="agent-install-controller-url"]').setValue('http://controller:57017')
    await wrapper.find('[data-test="agent-install-generate"]').trigger('click')

    expect(store.generateInstallCommand).toHaveBeenCalledWith('h1', {
      method: 'generated_command',
      controller_url: 'http://controller:57017',
      release_base_url: expect.stringMatching(versionedReleaseURL),
      remote_agent_port: 57017,
      transport_type: 'direct',
      token_ttl_minutes: 30,
    })
    expect(wrapper.text()).toContain('curl install')
  })

  it('runs SSH push as install/start then provision/connect automatically', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'installAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux/amd64',
      message: 'installed',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned' })
    vi.spyOn(store, 'checkAgent').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        host: hosts[0],
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('input[value="push_over_ssh"]').setValue(true)
    expect(wrapper.find('[data-test="install-phase-start"]').text()).toContain('安装并启动')
    expect(wrapper.find('[data-test="install-phase-security"]').text()).toContain('下发安全配置并连接')
    expect(wrapper.find('[data-test="agent-install-push"]').exists()).toBe(true)
    await wrapper.find('[data-test="agent-install-push"]').trigger('click')
    await flushPromises()

    expect(store.installAgent).toHaveBeenCalledWith('h1', { method: 'push_over_ssh', force_reinstall: false })
    expect(store.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'auto' })
    expect(store.checkAgent).toHaveBeenCalledWith('h1')
    expect(wrapper.find('[data-test="install-phase-security"]').text()).toContain('已连接')
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).not.toContain('locked')
  })

  it('blocks SSH push until the Host has a login credential and trusted host-key fingerprint', async () => {
    const store = useAgentsStore()
    const installAgent = vi.spyOn(store, 'installAgent')
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        host: {
          ...hosts[0],
          ssh_user: 'root',
          ssh_credential_configured: true,
          ssh_host_key_fingerprint_configured: false,
        },
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('input[value="push_over_ssh"]').setValue(true)

    expect(wrapper.get('[data-test="agent-install-push"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="agent-install-push-blocker"]').text()).toContain('host-key fingerprint')
    await wrapper.get('[data-test="agent-install-push"]').trigger('click')
    expect(installAgent).not.toHaveBeenCalled()
  })

  it('auto-restarts SSH push installs when auto TLS provision requires restart', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'installAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux/amd64',
      message: 'installed',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned', restart_required: true })
    vi.spyOn(store, 'restartAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux',
      message: 'restarted',
    })
    const checkAgent = vi.spyOn(store, 'checkAgent').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        host: hosts[0],
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('input[value="push_over_ssh"]').setValue(true)
    await wrapper.find('[data-test="agent-install-push"]').trigger('click')
    await flushPromises()

    expect(store.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'auto' })
    expect(store.restartAgent).toHaveBeenCalledWith('h1')
    expect(checkAgent).toHaveBeenCalledWith('h1')
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).not.toContain('locked')
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).toContain('active')
  })

  it('polls connection checks after SSH push restart until the Agent is reachable', async () => {
    vi.useFakeTimers()
    const store = useAgentsStore()
    vi.spyOn(store, 'installAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux/amd64',
      message: 'installed',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned', restart_required: true })
    vi.spyOn(store, 'restartAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux',
      message: 'restarted',
    })
    const checkAgent = vi.spyOn(store, 'checkAgent')
      .mockResolvedValueOnce(agent({ runtime: { installed: false, health: 'unreachable', reachable: false } }))
      .mockResolvedValueOnce(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        host: hosts[0],
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('input[value="push_over_ssh"]').setValue(true)
    const run = wrapper.find('[data-test="agent-install-push"]').trigger('click')
    await flushPromises()

    expect(checkAgent).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="install-phase-security"]').text()).toContain('1/45')

    await vi.advanceTimersByTimeAsync(2000)
    await run
    await flushPromises()

    expect(checkAgent).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).toContain('active')
  })

  it('stops restart polling after the panel is closed', async () => {
    vi.useFakeTimers()
    const store = useAgentsStore()
    vi.spyOn(store, 'installAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'darwin/arm64',
      message: 'installed',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned', restart_required: true })
    vi.spyOn(store, 'restartAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'darwin',
      message: 'restarted',
    })
    const checkAgent = vi.spyOn(store, 'checkAgent')
      .mockResolvedValueOnce(agent({ runtime: { installed: false, health: 'unreachable', reachable: false } }))
      .mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        host: hosts[0],
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('input[value="push_over_ssh"]').setValue(true)
    const run = wrapper.find('[data-test="agent-install-push"]').trigger('click')
    await flushPromises()

    expect(checkAgent).toHaveBeenCalledTimes(1)
    await wrapper.find('.settings-btn-ghost').trigger('click')
    expect(wrapper.emitted('cancel')).toBeTruthy()
    await wrapper.setProps({ visible: false })
    await vi.advanceTimersByTimeAsync(2000)
    await run
    await flushPromises()

    expect(checkAgent).toHaveBeenCalledTimes(1)
  })

  it('shows a manual retry button when restart polling exhausts and refreshes the connection', async () => {
    vi.useFakeTimers()
    const store = useAgentsStore()
    vi.spyOn(store, 'installAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux/amd64',
      message: 'installed',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned', restart_required: true })
    vi.spyOn(store, 'restartAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux',
      message: 'restarted',
    })
    const checkAgent = vi.spyOn(store, 'checkAgent')
    for (let i = 0; i < 45; i += 1) {
      checkAgent.mockResolvedValueOnce(agent({ runtime: { installed: false, health: 'unreachable', reachable: false } }))
    }
    checkAgent.mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        host: hosts[0],
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('input[value="push_over_ssh"]').setValue(true)
    const run = wrapper.find('[data-test="agent-install-push"]').trigger('click')
    await flushPromises()
    for (let i = 1; i < 45; i += 1) {
      await vi.advanceTimersByTimeAsync(2000)
    }
    await run
    await flushPromises()

    expect(wrapper.find('[data-test="agent-install-security-retry"]').exists()).toBe(true)

    await wrapper.find('[data-test="agent-install-security-retry"]').trigger('click')
    await flushPromises()

    expect(checkAgent).toHaveBeenCalledTimes(46)
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).toContain('active')
  })

  it('waits for generated command execution before provisioning and connecting', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'generateInstallCommand').mockResolvedValue({
      command: 'curl install',
      expires_at: '2026-06-08T10:30:00Z',
      token_id: 'tok_1',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned' })
    vi.spyOn(store, 'checkAgent')
      .mockResolvedValueOnce(agent({ runtime: { installed: true, health: 'pending-bootstrap', reachable: true } }))
      .mockResolvedValueOnce(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="agent-install-generate"]').trigger('click')
    await flushPromises()
    expect(store.provisionAgent).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="agent-install-command-executed"]').exists()).toBe(true)

    await wrapper.find('[data-test="agent-install-command-executed"]').trigger('click')
    await flushPromises()

    expect(store.checkAgent).toHaveBeenCalledWith('h1')
    expect(store.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'auto' })
    expect(wrapper.find('[data-test="install-phase-start"]').text()).toContain('已启动')
    expect(wrapper.find('[data-test="install-phase-security"]').text()).toContain('已连接')
  })

  it('shows generated restart command and unlocks probe after manual restart check', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'generateInstallCommand').mockResolvedValue({
      command: 'curl install',
      restart_command: 'sudo -n systemctl restart superdev-agent.service',
      expires_at: '2026-06-08T10:30:00Z',
      token_id: 'tok_1',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned', restart_required: true })
    vi.spyOn(store, 'checkAgent')
      .mockResolvedValueOnce(agent({ runtime: { installed: true, health: 'pending-bootstrap', reachable: true } }))
      .mockResolvedValueOnce(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        initialTab: 'install',
      },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="agent-install-generate"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="agent-install-command-executed"]').trigger('click')
    await flushPromises()

    expect(store.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'auto' })
    expect(wrapper.find('[data-test="agent-restart-command"]').text()).toContain('systemctl restart superdev-agent.service')
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).not.toContain('active')

    await wrapper.find('[data-test="agent-restart-command-executed"]').trigger('click')
    await flushPromises()

    expect(store.checkAgent).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).not.toContain('locked')
    expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).toContain('active')
  })

  it('saves ordered transport chain and tests one entry', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentTransport').mockResolvedValue(agent())
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'reachable',
      reachable: true,
      latency_ms: 7,
      checked_at: '2026-06-07T10:00:00Z',
    })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'transport' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="transport-test-0"]').trigger('click')
    expect(store.testTransport).toHaveBeenCalledWith('h1', 0)
    expect(wrapper.text()).toContain('reachable')

    await wrapper.find('[data-test="transport-move-down-0"]').trigger('click')
    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')
    expect(store.updateAgentTransport).toHaveBeenCalledWith('h1', {
      transport: {
        chain: [
          { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
          { type: 'direct', direct: { address: '100.64.0.8:57017' } },
        ],
      },
    })
  })

  it('hides transport probe controls until the agent is installed', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'reachable',
      reachable: true,
      checked_at: '2026-06-07T10:00:00Z',
    })
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        initialTab: 'transport',
      },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="transport-test-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="transport-entry-0"] .probe-result').exists()).toBe(false)
    expect(wrapper.find('[data-test="transport-entry-0"]').text()).not.toContain('未测')
  })

  it('lets direct transport fill the editable address from host IP tags', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentTransport').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ transport: { chain: [{ type: 'direct', direct: { address: '203.0.113.10:57017' } }] } }),
        host: hosts[0],
        initialTab: 'transport',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="direct-address-select-0"]').exists()).toBe(false)
    expect((wrapper.find('[data-test="direct-address-input-0"]').element as HTMLInputElement).value).toBe('203.0.113.10:57017')
    expect(wrapper.find('[data-test="direct-address-option-public_ip-0"]').text()).toContain('203.0.113.10:57017')
    await wrapper.find('[data-test="direct-address-option-private_ip-0"]').trigger('click')
    expect((wrapper.find('[data-test="direct-address-input-0"]').element as HTMLInputElement).value).toBe('10.0.0.8:57017')
    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')

    expect(store.updateAgentTransport).toHaveBeenCalledWith('h1', {
      transport: {
        chain: [
          { type: 'direct', direct: { address: '10.0.0.8:57017' } },
        ],
      },
    })
  })

  it('keeps direct custom address editable even when host IP tags exist', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentTransport').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ transport: { chain: [{ type: 'direct', direct: { address: '203.0.113.10:57017' } }] } }),
        host: hosts[0],
        initialTab: 'transport',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="direct-address-input-0"]').setValue('agent.example.com:57017')
    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')

    expect(store.updateAgentTransport).toHaveBeenCalledWith('h1', {
      transport: { chain: [{ type: 'direct', direct: { address: 'agent.example.com:57017' } }] },
    })
  })

  it('falls back to a custom direct address when the host has no recorded IP options', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentTransport').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ transport: { chain: [{ type: 'direct', direct: { address: '' } }] } }),
        host: { id: 'h1', name: 'ali-01', tags: [] },
        initialTab: 'transport',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="direct-address-input-0"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="direct-address-option-public_ip-0"]').exists()).toBe(false)
    await wrapper.find('[data-test="direct-address-input-0"]').setValue('agent.example.com:57017')
    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')

    expect(store.updateAgentTransport).toHaveBeenCalledWith('h1', {
      transport: { chain: [{ type: 'direct', direct: { address: 'agent.example.com:57017' } }] },
    })
  })

  it('shows a reinstall hint when transport edits change the derived bind scope', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent({ transport: { chain: [{ type: 'tunnel', tunnel: { remote_agent_port: 57017 } }] } }), host: hosts[0], initialTab: 'transport' },
      global: { plugins: [installTestI18n()] },
    })

    expect(wrapper.find('[data-test="agent-bind-scope-dirty"]').exists()).toBe(false)
    await wrapper.find('[data-test="transport-add-direct"]').trigger('click')
    expect(wrapper.find('[data-test="agent-bind-scope-dirty"]').text()).toContain('重新安装')
  })

  it('warns before a tunnel target edit invalidates the active tunnel runtime', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent(),
        host: hosts[0],
        initialTab: 'transport',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-transport-tunnel-invalidation"]').exists()).toBe(false)
    await wrapper.find('[data-test="transport-remove-1"]').trigger('click')

    expect(wrapper.get('[data-test="agent-transport-tunnel-invalidation"]').text()).toContain('disconnect')
  })

  it('locks transport probes while local chain edits are unsaved', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'reachable',
      reachable: true,
      checked_at: '2026-06-07T10:00:00Z',
    })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'transport' },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="transport-move-down-0"]').trigger('click')

    expect(wrapper.find('[data-test="agent-transport-dirty"]').text()).toContain('保存连接链后再测试')
    expect(wrapper.find('[data-test="transport-test-0"]').attributes('disabled')).toBeDefined()
    await wrapper.find('[data-test="transport-test-0"]').trigger('click')
    expect(store.testTransport).not.toHaveBeenCalled()
  })

  it('clears saved-route and probe-result labels when chain edits are unsaved', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'reachable',
      reachable: true,
      latency_ms: 7,
      checked_at: '2026-06-07T10:00:00Z',
    })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), node: node(), initialTab: 'transport' },
      global: { plugins: [installTestI18n()] },
    })

    await wrapper.find('[data-test="transport-test-0"]').trigger('click')
    expect(wrapper.find('[data-test="transport-entry-0"] .probe-result').text()).toContain('reachable')

    await wrapper.find('[data-test="transport-move-down-0"]').trigger('click')
    expect(wrapper.find('[data-test="transport-entry-0"] .probe-result').text()).toContain('未测')

    await wrapper.find('[data-test="agent-panel-tab-probe"]').trigger('click')
    expect(wrapper.find('[data-test="agent-probe-dirty"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-probe-result-1"]').text()).toContain('未测')
  })

  it('locks probe tab until agent is installed', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        initialTab: 'probe',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-probe-locked"]').exists()).toBe(true)
    await wrapper.find('[data-test="agent-probe-go-install"]').trigger('click')
    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
  })

  it('runs full probe and refreshes health when installed', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport')
      .mockResolvedValueOnce({ index: 0, transport_type: 'direct', status: 'unreachable', reachable: false, error: 'connection refused', checked_at: '2026-06-07T10:00:00Z' })
      .mockResolvedValueOnce({ index: 1, transport_type: 'tunnel', status: 'reachable', reachable: true, latency_ms: 8, checked_at: '2026-06-07T10:00:01Z' })
    vi.spyOn(store, 'checkAgent').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), node: node(), initialTab: 'probe' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="agent-probe-run"]').trigger('click')

    expect(store.testTransport).toHaveBeenCalledWith('h1', 0)
    expect(store.testTransport).toHaveBeenCalledWith('h1', 1)
    expect(store.checkAgent).toHaveBeenCalledWith('h1')
    expect(wrapper.find('[data-test="agent-probe-result-1"]').text()).toContain('Tunnel · :57017')
    expect(wrapper.find('[data-test="agent-probe-result-1"]').text()).toContain('reachable')
  })

  describe('existing agent detected (adoption)', () => {
    async function mountAtInstallBlockedByExistingAgent() {
      const store = useAgentsStore()
      const installAgent = vi.spyOn(store, 'installAgent').mockRejectedValueOnce(existingAgentDetectedError('1.4.0'))
      const wrapper = mount(AgentConfigPanel, {
        props: {
          visible: true,
          agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
          host: hosts[0],
          initialTab: 'install',
        },
        global: { plugins: [installTestI18n()] },
      })
      await wrapper.find('input[value="push_over_ssh"]').setValue(true)
      await wrapper.find('[data-test="agent-install-push"]').trigger('click')
      await flushPromises()
      return { store, wrapper, installAgent }
    }

    it('shows the existing-agent branch with version and offers adopt/force-reinstall on 409', async () => {
      const { wrapper } = await mountAtInstallBlockedByExistingAgent()

      expect(wrapper.find('[data-test="agent-install-existing-detected"]').text()).toContain('1.4.0')
      expect(wrapper.find('[data-test="agent-adopt-start"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="agent-force-reinstall"]').exists()).toBe(true)
      // 正常安装/启动阶段的文案不应该同时出现，避免用户误以为安装仍在正常推进。
      expect(wrapper.find('[data-test="install-phase-start"]').text()).not.toContain('安装并启动 Agent')
    })

    it('adopts: request → 2s poll → approved → exchange → save credential → connect', async () => {
      vi.useFakeTimers()
      const { store, wrapper } = await mountAtInstallBlockedByExistingAgent()

      const requestAdoption = vi.spyOn(store, 'requestAdoption').mockResolvedValue({
        id: 'req-1',
        state: 'pending',
        expires_at: '2099-01-01T00:00:00Z',
      })
      const getAdoptionStatus = vi.spyOn(store, 'getAdoptionStatus')
        .mockResolvedValueOnce({ state: 'pending' })
        .mockResolvedValueOnce({ state: 'approved', adoption_token: 'one-time-token' })
      const exchangeAdoption = vi.spyOn(store, 'exchangeAdoption').mockResolvedValue({
        token: 'long-term-token',
        record: { id: 'rec-1', name: 'CP-New', hash: 'h', issued_at: '2026-01-01T00:00:00Z' },
      })
      const adoptAgentCredential = vi.spyOn(store, 'adoptAgentCredential').mockResolvedValue({ status: 'provisioned' })
      const checkAgent = vi.spyOn(store, 'checkAgent').mockResolvedValue(agent())

      await wrapper.find('[data-test="agent-adopt-start"]').trigger('click')
      await flushPromises()

      expect(requestAdoption).toHaveBeenCalledWith('http://203.0.113.10:57017', expect.any(String))
      expect(getAdoptionStatus).toHaveBeenCalledTimes(1)
      expect(getAdoptionStatus).toHaveBeenCalledWith('http://203.0.113.10:57017', 'req-1')
      expect(wrapper.find('[data-test="agent-adopt-waiting"]').exists()).toBe(true)
      expect(exchangeAdoption).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(2000)
      await flushPromises()

      expect(getAdoptionStatus).toHaveBeenCalledTimes(2)
      expect(exchangeAdoption).toHaveBeenCalledWith('http://203.0.113.10:57017', 'req-1', 'one-time-token')
      expect(adoptAgentCredential).toHaveBeenCalledWith('h1', 'long-term-token')
      expect(checkAgent).toHaveBeenCalledWith('h1')
      expect(wrapper.find('[data-test="agent-install-existing-detected"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="agent-panel-tab-probe"]').classes()).toContain('active')
    })

    it('shows a clear, non-silent failure when the adoption request is rejected', async () => {
      const { store, wrapper } = await mountAtInstallBlockedByExistingAgent()
      vi.spyOn(store, 'requestAdoption').mockResolvedValue({ id: 'req-1', state: 'pending', expires_at: '2099-01-01T00:00:00Z' })
      vi.spyOn(store, 'getAdoptionStatus').mockResolvedValue({ state: 'rejected' })

      await wrapper.find('[data-test="agent-adopt-start"]').trigger('click')
      await flushPromises()

      expect(wrapper.find('[data-test="agent-adopt-failed"]').text()).toContain('拒绝')
      // 失败后必须能重新发起，不能卡死在轮询态。
      expect(wrapper.find('[data-test="agent-adopt-start"]').exists()).toBe(true)
    })

    it('shows a clear, non-silent failure when the adoption request expires', async () => {
      const { store, wrapper } = await mountAtInstallBlockedByExistingAgent()
      vi.spyOn(store, 'requestAdoption').mockResolvedValue({ id: 'req-1', state: 'pending', expires_at: '2099-01-01T00:00:00Z' })
      vi.spyOn(store, 'getAdoptionStatus').mockResolvedValue({ state: 'expired' })

      await wrapper.find('[data-test="agent-adopt-start"]').trigger('click')
      await flushPromises()

      expect(wrapper.find('[data-test="agent-adopt-failed"]').text()).toContain('过期')
      expect(wrapper.find('[data-test="agent-adopt-start"]').exists()).toBe(true)
    })

    it('stops polling when the user cancels adoption (termination condition)', async () => {
      vi.useFakeTimers()
      const { store, wrapper } = await mountAtInstallBlockedByExistingAgent()
      vi.spyOn(store, 'requestAdoption').mockResolvedValue({ id: 'req-1', state: 'pending', expires_at: '2099-01-01T00:00:00Z' })
      const getAdoptionStatus = vi.spyOn(store, 'getAdoptionStatus').mockResolvedValue({ state: 'pending' })

      await wrapper.find('[data-test="agent-adopt-start"]').trigger('click')
      await flushPromises()
      expect(getAdoptionStatus).toHaveBeenCalledTimes(1)

      await wrapper.find('[data-test="agent-adopt-cancel"]').trigger('click')
      await vi.advanceTimersByTimeAsync(10000)
      await flushPromises()

      expect(getAdoptionStatus).toHaveBeenCalledTimes(1)
      expect(wrapper.find('[data-test="agent-adopt-start"]').exists()).toBe(true)
    })

    it('stops polling when the panel is closed (termination condition)', async () => {
      vi.useFakeTimers()
      const { store, wrapper } = await mountAtInstallBlockedByExistingAgent()
      vi.spyOn(store, 'requestAdoption').mockResolvedValue({ id: 'req-1', state: 'pending', expires_at: '2099-01-01T00:00:00Z' })
      const getAdoptionStatus = vi.spyOn(store, 'getAdoptionStatus').mockResolvedValue({ state: 'pending' })

      await wrapper.find('[data-test="agent-adopt-start"]').trigger('click')
      await flushPromises()
      expect(getAdoptionStatus).toHaveBeenCalledTimes(1)

      await wrapper.setProps({ visible: false })
      await vi.advanceTimersByTimeAsync(10000)
      await flushPromises()

      expect(getAdoptionStatus).toHaveBeenCalledTimes(1)
    })

    it('requires an explicit, non-understated confirmation before force-reinstalling', async () => {
      const { store, wrapper, installAgent } = await mountAtInstallBlockedByExistingAgent()
      installAgent.mockResolvedValueOnce({ ok: true, host_id: 'h1', platform: 'linux/amd64', message: 'installed' })
      vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned' })
      vi.spyOn(store, 'checkAgent').mockResolvedValue(agent())

      expect(wrapper.get('[data-test="agent-force-reinstall"]').attributes('disabled')).toBeDefined()

      await wrapper.find('[data-test="agent-force-reinstall-confirm"]').setValue(true)

      expect(wrapper.find('[data-test="agent-force-reinstall-warning"]').text()).toContain('停止')
      expect(wrapper.get('[data-test="agent-force-reinstall"]').attributes('disabled')).toBeUndefined()

      await wrapper.find('[data-test="agent-force-reinstall"]').trigger('click')
      await flushPromises()

      expect(installAgent).toHaveBeenLastCalledWith('h1', { method: 'push_over_ssh', force_reinstall: true })
      expect(wrapper.find('[data-test="agent-install-existing-detected"]').exists()).toBe(false)
    })
  })
})
