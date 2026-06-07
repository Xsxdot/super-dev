/**
 * AgentSecurityPanel unified configuration tests.
 *
 * Responsibilities:
 *   - Verify Agent-level TLS modes are edited outside transport entries
 *   - Verify unified config is saved through the Agent config API
 *   - Verify provision security is a single Agent-level action
 *
 * Boundaries:
 *   - Does not call the real backend
 *   - Does not edit transport chain fields
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentSecurityPanel from '../AgentSecurityPanel.vue'
import { useAgentsStore } from '@/stores/agents'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentDTO } from '@/api/agent'

function agent(): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: [],
    transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }] },
    config: { listen_address: '127.0.0.1', listen_port: 57017 },
    runtime: { installed: false, health: 'unknown', reachable: false },
    security: {
      token_configured: false,
      provision_state: 'pending-bootstrap',
      tls: { mode: 'auto', server_name: 'ali-01' },
    },
  }
}

describe('AgentSecurityPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('saves unified config and provisions security once for the agent', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentConfig').mockResolvedValue(agent())
    vi.spyOn(store, 'provisionAgent').mockResolvedValue({ status: 'provisioned' })
    const wrapper = mount(AgentSecurityPanel, {
      props: { visible: true, agent: agent() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="agent-tls-mode-manual"]').setValue(true)
    await wrapper.find('[data-test="agent-ca-cert"]').setValue('PEM')
    await wrapper.find('[data-test="agent-security-save"]').trigger('click')

    expect(store.updateAgentConfig).toHaveBeenCalledWith('h1', expect.objectContaining({
      config: expect.objectContaining({ listen_port: 57017 }),
      security: expect.objectContaining({
        tls: expect.objectContaining({ mode: 'manual', ca_cert: 'PEM' }),
      }),
    }))

    await wrapper.find('[data-test="agent-provision-security"]').trigger('click')

    expect(store.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'manual' })
    expect(wrapper.find('[data-test^="transport-provision-"]').exists()).toBe(false)
  })
})
