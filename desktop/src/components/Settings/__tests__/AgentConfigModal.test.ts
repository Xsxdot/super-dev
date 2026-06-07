/**
 * AgentConfigModal chain editor tests.
 *
 * Responsibilities:
 *   - Verify ordered transport entries can be added, removed, and moved
 *   - Verify submit emits TransportConfig.chain
 *   - Verify per-entry test/provision actions call the agents store
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
        { type: 'direct', direct: { address: '100.64.0.8:57017', tls: true, ca_cert: 'PEM' } },
        { type: 'tunnel', tunnel: { ssh_host: '10.0.0.8', ssh_port: 22, ssh_user: 'root', remote_agent_port: 57017 } },
      ],
    },
    runtime: { installed: false, health: 'unknown', reachable: false },
    security: { token_configured: false, provision_state: 'pending-bootstrap' },
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

  it('calls test and provision actions for one entry', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'pending-bootstrap',
      reachable: true,
      checked_at: '2026-06-07T10:00:00Z',
    })
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned' })
    const wrapper = mount(AgentConfigModal, {
      props: { visible: true, agent: agent() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="transport-test-0"]').trigger('click')
    expect(store.testTransport).toHaveBeenCalledWith('h1', 0)
    expect(wrapper.text()).toContain('pending-bootstrap')

    await wrapper.find('[data-test="transport-provision-0"]').trigger('click')
    expect(store.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'auto' })
  })
})
