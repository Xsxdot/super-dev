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
      generateAgentInstallCommand: vi.fn(),
    },
  }
})

function agent(hostId = 'h1'): AgentDTO {
  return {
    host_id: hostId,
    host_name: 'ali-01',
    tags: ['prod'],
    transport: { type: 'direct', direct: { address: '100.64.0.8:57017', tls: false } },
    runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
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
      transport: { type: 'direct', direct: { address: '100.64.0.9:57017', tls: false } },
    })

    expect(api.updateAgent).toHaveBeenCalledWith('h1', expect.objectContaining({
      transport: expect.objectContaining({ type: 'direct' }),
    }))
  })
})
