/**
 * AgentConfigModal chain editor tests.
 *
 * Responsibilities:
 *   - Verify ordered transport entries can be added, removed, and moved
 *   - Verify submit emits TransportConfig.chain
 *   - Verify transport editor does not expose Agent security or Host SSH fields
 *   - Verify per-entry test action calls the agents store
 *
 * Boundaries:
 *   - Does not call the real backend
 *   - Does not open install command modal
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentConfigModal from '../AgentConfigModal.vue'
import { useAgentsStore } from '@/stores/agents'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentDTO } from '@/api/agent'

function agent(): AgentDTO {
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
    runtime: { installed: false, health: 'unknown', reachable: false },
    config: { listen_port: 57017 },
    security: { token_configured: false, provision_state: 'pending-bootstrap', tls: { mode: 'manual', ca_cert: 'PEM' } },
  }
}

describe('AgentConfigModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('submits an ordered transport chain', async () => {
    const wrapper = mount(AgentConfigModal, {
      props: { visible: true, agent: agent() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="transport-move-down-0"]').trigger('click')
    await wrapper.find('[data-test="agent-config-submit"]').trigger('click')

    const payload = wrapper.emitted('submit')?.[0][0] as any
    expect(payload.transport.chain.map((entry: any) => entry.type)).toEqual(['tunnel', 'direct'])
  })

  it('adds and removes transport entries', async () => {
    const wrapper = mount(AgentConfigModal, {
      props: { visible: true, agent: agent() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="transport-remove-1"]').trigger('click')
    expect(wrapper.findAll('[data-test^="transport-entry-"]')).toHaveLength(1)
    await wrapper.find('[data-test="transport-add-tunnel"]').trigger('click')
    expect(wrapper.findAll('[data-test^="transport-entry-"]')).toHaveLength(2)
  })

  it('only exposes transport-specific fields', async () => {
    const wrapper = mount(AgentConfigModal, {
      props: { visible: true, agent: agent() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="direct-tls-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="direct-ca-cert-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tunnel-ssh-host-1"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tunnel-remote-agent-port-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="transport-provision-0"]').exists()).toBe(false)
  })

  it('calls test action for one entry', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'pending-bootstrap',
      reachable: true,
      checked_at: '2026-06-07T10:00:00Z',
    })
    const wrapper = mount(AgentConfigModal, {
      props: { visible: true, agent: agent() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="transport-test-0"]').trigger('click')
    expect(store.testTransport).toHaveBeenCalledWith('h1', 0)
    expect(wrapper.text()).toContain('pending-bootstrap')
  })
})
