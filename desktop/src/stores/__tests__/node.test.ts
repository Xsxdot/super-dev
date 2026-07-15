/**
 * nodeStore tests verify the frontend NodeRegistry subscription cache.
 *
 * Responsibilities:
 *   - Bootstrap from /api/nodes
 *   - Merge /ws/nodes snapshots
 *   - Convert node snapshots into HostManagedDeploymentStatus fallback shape
 *   - Keep the shared websocket open while any page still owns it
 *
 * Boundaries:
 *   - Does not render UI
 *   - Does not connect to a real Go agent
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { api, type NodeStatus } from '@/api/agent'
import { useNodeStore } from '@/stores/node'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listNodes: vi.fn(),
    },
    nodesWsUrl: vi.fn(() => 'ws://agent/ws/nodes'),
  }
})

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  url: string

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.closed = true
    this.onclose?.()
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) })
  }

  serverClose() {
    this.onclose?.()
  }
}

function status(hostId: string, reachable = true): NodeStatus {
  return {
    host_id: hostId,
    name: hostId === 'h1' ? 'ali-01' : 'jp',
    reachable,
    agent: {
      installed: reachable,
      version: reachable ? '0.1.0' : undefined,
      health: reachable ? 'healthy' : 'unreachable',
      reachable,
    },
    deployments: [],
    managed: reachable
      ? {
          deployment_count: 1,
          collector_count: 1,
          active_collector_count: 1,
          collectors: [],
          last_result: { deployment_count: 1, collector_count: 1, persisted: true },
        }
      : undefined,
    updated_at: '2026-06-06T10:00:00Z',
    error: reachable ? undefined : 'node status stream closed',
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  FakeWebSocket.instances = []
  vi.stubGlobal('WebSocket', FakeWebSocket)
  vi.mocked(api.listNodes).mockResolvedValue([])
})

afterEach(() => {
  vi.useRealTimers()
})

describe('nodeStore', () => {
  it('loads initial nodes and opens one websocket', async () => {
    vi.mocked(api.listNodes).mockResolvedValue([status('h1')])
    const store = useNodeStore()

    await store.start()

    expect(store.nodeOf('h1')?.name).toBe('ali-01')
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(FakeWebSocket.instances[0].url).toBe('ws://agent/ws/nodes')
  })

  it('merges websocket snapshots by host id', async () => {
    const store = useNodeStore()
    await store.start()

    FakeWebSocket.instances[0].emit([status('h2')])
    await nextTick()

    expect(store.nodesList.map(node => node.host_id)).toEqual(['h2'])
  })

  it('converts node status to host managed status shape', async () => {
    const store = useNodeStore()
    await store.start()

    FakeWebSocket.instances[0].emit([status('h1'), status('h2', false)])
    await nextTick()

    const h1 = store.managedStatuses.get('h1')
    expect(h1?.tunnel_connected).toBe(true)
    expect(h1?.remote?.deployment_count).toBe(1)
    const h2 = store.managedStatuses.get('h2')
    expect(h2?.tunnel_connected).toBe(false)
    expect(h2?.error).toContain('node status stream closed')
  })

  it('converts null deployments from unreachable node snapshots as zero desired deployments', async () => {
    const store = useNodeStore()
    await store.start()

    FakeWebSocket.instances[0].emit([{
      ...status('h2', false),
      deployments: null as unknown as NodeStatus['deployments'],
    }])
    await nextTick()

    expect(store.managedStatuses.get('h2')?.desired_deployment_count).toBe(0)
  })

  it('keeps websocket open until all callers stop', async () => {
    const store = useNodeStore()
    await store.start()
    await store.start()
    FakeWebSocket.instances[0].onopen?.()

    store.stop()
    expect(FakeWebSocket.instances[0].closed).toBe(false)
    expect(store.connected).toBe(true)

    store.stop()
    expect(FakeWebSocket.instances[0].closed).toBe(true)
    expect(store.connected).toBe(false)
  })

  it('stop closes websocket and clears connected flag', async () => {
    const store = useNodeStore()
    await store.start()
    store.stop()
    expect(FakeWebSocket.instances[0].closed).toBe(true)
    expect(store.connected).toBe(false)
  })

  it('reconnects after stream closes while consumers are active', async () => {
    vi.useFakeTimers()
    const store = useNodeStore()
    await store.start()
    FakeWebSocket.instances[0].onopen?.()

    FakeWebSocket.instances[0].serverClose()
    await vi.advanceTimersByTimeAsync(500)

    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(api.listNodes).toHaveBeenCalledTimes(2)
  })

  it('exposes agent runtime and node errors by host id', async () => {
    const store = useNodeStore()
    await store.start()

    FakeWebSocket.instances[0].emit([status('h1'), status('h2', false)])
    await nextTick()

    expect(store.agentRuntimeOf('h1')?.health).toBe('healthy')
    expect(store.agentRuntimeOf('h2')?.reachable).toBe(false)
    expect(store.nodeErrorOf('h1')).toBeUndefined()
    expect(store.nodeErrorOf('h2')).toContain('node status stream closed')
  })
})
