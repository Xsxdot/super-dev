/**
 * agentsStore tests verify host-agent connection configuration state.
 *
 * Responsibilities:
 *   - Load first-class Agent records by host id
 *   - Send transport updates through the Agent API
 *   - Keep generated install commands out of Host CRUD state
 *
 * Boundaries:
 *   - Does not render the Settings UI
 *   - Does not connect to a real Go agent
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api, type AgentDTO } from '@/api/agent'
import { useAgentsStore } from '../agents'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listAgents: vi.fn(),
      updateAgent: vi.fn(),
      deleteAgent: vi.fn(),
      checkAgent: vi.fn(),
      generateAgentInstallCommand: vi.fn(),
      testAgentTransport: vi.fn(),
      provisionAgent: vi.fn(),
    },
  }
})

function agent(hostId = 'h1'): AgentDTO {
  return {
    host_id: hostId,
    host_name: 'ali-01',
    tags: ['prod'],
    transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017', tls: false } }] },
    runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    security: { token_configured: false, provision_state: 'not-configured' },
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(api.listAgents).mockResolvedValue([])
})

describe('agents store', () => {
  it('loads agents by host id', async () => {
    vi.mocked(api.listAgents).mockResolvedValue([agent()])
    const store = useAgentsStore()

    await store.loadAgents()

    expect(store.agentOf('h1')?.host_name).toBe('ali-01')
  })

  it('updates transport config through agent api', async () => {
    vi.mocked(api.updateAgent).mockResolvedValue(agent('h1'))
    const store = useAgentsStore()

    await store.updateAgent('h1', {
      transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.9:57017', tls: false } }] },
    })

    expect(api.updateAgent).toHaveBeenCalledWith('h1', expect.objectContaining({
      transport: expect.objectContaining({ chain: expect.any(Array) }),
    }))
  })

  it('checks runtime through first-class agent api', async () => {
    vi.mocked(api.checkAgent).mockResolvedValue(agent('h1'))
    const store = useAgentsStore()

    await expect(store.checkAgent('h1')).resolves.toMatchObject({ host_id: 'h1' })

    expect(api.checkAgent).toHaveBeenCalledWith('h1')
    expect(store.agentOf('h1')?.runtime.health).toBe('healthy')
  })

  it('deletes agent config through first-class agent api', async () => {
    vi.mocked(api.deleteAgent).mockResolvedValue(undefined)
    const store = useAgentsStore()
    store.agents = [agent('h1'), agent('h2')]

    await store.deleteAgent('h1')

    expect(api.deleteAgent).toHaveBeenCalledWith('h1')
    expect(store.agentOf('h1')).toBeUndefined()
    expect(store.agentOf('h2')?.host_id).toBe('h2')
  })

  it('tests and provisions agent transports through the api', async () => {
    const store = useAgentsStore()
    vi.mocked(api.testAgentTransport).mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'reachable',
      reachable: true,
      checked_at: '2026-06-07T10:00:00Z',
    })
    vi.mocked(api.provisionAgent).mockResolvedValue({ status: 'provisioned' })
    vi.mocked(api.listAgents).mockResolvedValue([])

    await expect(store.testTransport('h1', 0)).resolves.toMatchObject({ status: 'reachable' })
    await expect(store.provisionAgent('h1', { index: 0, tls_mode: 'off' })).resolves.toMatchObject({ status: 'provisioned' })

    expect(api.testAgentTransport).toHaveBeenCalledWith('h1', { index: 0 })
    expect(api.provisionAgent).toHaveBeenCalledWith('h1', { index: 0, tls_mode: 'off' })
  })
})
