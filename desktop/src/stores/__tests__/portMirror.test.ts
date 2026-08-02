/**
 * portMirrorStore tests verify the frontend port-mirror subscription cache and event diff.
 *
 * Responsibilities:
 *   - Bootstrap from /api/port-mirrors
 *   - Merge /ws/port-mirrors snapshots
 *   - Diff old vs new snapshot into MirrorEvent (established/conflict/failed/removed)
 *   - Keep the shared websocket open while any page still owns it
 *
 * Boundaries:
 *   - Does not render UI
 *   - Does not connect to a real Go agent
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api, type MirrorStatus } from '@/api/agent'
import { usePortMirrorStore } from '@/stores/portMirror'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listPortMirrors: vi.fn(),
      retryPortMirror: vi.fn(),
      stopMirrorOccupier: vi.fn(),
    },
    portMirrorWsUrl: vi.fn(() => Promise.resolve('ws://agent/ws/port-mirrors')),
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

function mirror(overrides: Partial<MirrorStatus> = {}): MirrorStatus {
  return {
    host_id: 'h1',
    host_name: 'dev-box',
    deployment_id: 'dep1',
    service_name: 'api',
    port: 3000,
    state: 'active',
    updated_at: '2026-06-06T10:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  FakeWebSocket.instances = []
  vi.stubGlobal('WebSocket', FakeWebSocket)
  vi.mocked(api.listPortMirrors).mockResolvedValue([])
})

afterEach(() => {
  vi.useRealTimers()
})

describe('portMirrorStore', () => {
  describe('snapshot diff', () => {
    it('applySnapshot 后 mirrorsForDeployment 命中', () => {
      const store = usePortMirrorStore()

      store.applySnapshot([
        mirror({ deployment_id: 'dep1', port: 3000 }),
        mirror({ deployment_id: 'dep2', port: 4000 }),
      ])

      expect(store.mirrorsForDeployment('dep1')).toHaveLength(1)
      expect(store.mirrorsForDeployment('dep1')[0].port).toBe(3000)
      expect(store.mirrorsForDeployment('dep2')).toHaveLength(1)
      expect(store.mirrorsForDeployment('dep3')).toHaveLength(0)
    })

    it('mirrorsForHost 按 host_id 过滤', () => {
      const store = usePortMirrorStore()

      store.applySnapshot([
        mirror({ host_id: 'h1', deployment_id: 'dep1', port: 3000 }),
        mirror({ host_id: 'h2', deployment_id: 'dep2', port: 4000 }),
      ])

      expect(store.mirrorsForHost('h1')).toHaveLength(1)
      expect(store.mirrorsForHost('h2')).toHaveLength(1)
    })

    it('首次出现的 active 条目产出 established 事件', () => {
      const store = usePortMirrorStore()

      store.applySnapshot([mirror({ state: 'active' })])

      expect(store.events).toHaveLength(1)
      expect(store.events[0]).toMatchObject({ deploymentId: 'dep1', port: 3000, hostName: 'dev-box', kind: 'established' })
      expect(store.events[0].at).toBeTypeOf('number')
    })

    it('active→conflict 跃迁产出 conflict 事件', () => {
      const store = usePortMirrorStore()
      store.applySnapshot([mirror({ state: 'active' })])

      store.applySnapshot([
        mirror({
          state: 'conflict',
          error: 'port_mirror_conflict',
          occupier: { pid: 123, name: 'node', started_at: '2026-06-06T10:00:00Z' },
        }),
      ])

      const conflictEvents = store.events.filter(e => e.kind === 'conflict')
      expect(conflictEvents).toHaveLength(1)
      expect(conflictEvents[0]).toMatchObject({ deploymentId: 'dep1', port: 3000, hostName: 'dev-box' })
    })

    it('条目消失产出 removed 事件', () => {
      const store = usePortMirrorStore()
      store.applySnapshot([mirror({ state: 'active' })])

      store.applySnapshot([])

      expect(store.mirrorsForDeployment('dep1')).toHaveLength(0)
      const removedEvents = store.events.filter(e => e.kind === 'removed')
      expect(removedEvents).toHaveLength(1)
      expect(removedEvents[0]).toMatchObject({ deploymentId: 'dep1', port: 3000, hostName: 'dev-box' })
    })

    it('状态不变时不产出新事件', () => {
      const store = usePortMirrorStore()
      store.applySnapshot([mirror({ state: 'active' })])
      const countAfterFirst = store.events.length

      store.applySnapshot([mirror({ state: 'active', updated_at: '2026-06-06T10:05:00Z' })])

      expect(store.events).toHaveLength(countAfterFirst)
    })

    it('同 host 同端口的两个 deployment 各自独立寻址，不会互相覆盖', () => {
      const store = usePortMirrorStore()

      store.applySnapshot([
        mirror({ deployment_id: 'winner', port: 3000, state: 'active' }),
        mirror({ deployment_id: 'loser', port: 3000, state: 'failed', error: 'duplicate_port_declaration' }),
      ])

      expect(store.mirrorsForHost('h1')).toHaveLength(2)
      const kinds = store.events.map(e => `${e.deploymentId}:${e.kind}`).sort()
      expect(kinds).toEqual(['loser:failed', 'winner:established'])
    })

    it('events 超过 200 条时按环形裁剪只保留最近 200 条', () => {
      const store = usePortMirrorStore()
      const snapshot: MirrorStatus[] = []
      for (let i = 0; i < 210; i++) {
        snapshot.push(mirror({ deployment_id: `dep${i}`, port: 3000 + i, state: 'active' }))
      }

      store.applySnapshot(snapshot)

      expect(store.events).toHaveLength(200)
      expect(store.events[0].port).toBe(3010)
      expect(store.events[199].port).toBe(3209)
    })
  })

  describe('websocket lifecycle', () => {
    it('loads initial mirrors and opens one websocket', async () => {
      vi.mocked(api.listPortMirrors).mockResolvedValue([mirror()])
      const store = usePortMirrorStore()

      await store.start()

      expect(store.mirrors).toHaveLength(1)
      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(FakeWebSocket.instances[0].url).toBe('ws://agent/ws/port-mirrors')
    })

    it('initial load does not itself produce events (baseline, not a transition)', async () => {
      vi.mocked(api.listPortMirrors).mockResolvedValue([mirror({ state: 'active' })])
      const store = usePortMirrorStore()

      await store.start()

      expect(store.events).toHaveLength(0)
    })

    it('merges websocket snapshots and updates mirrors', async () => {
      const store = usePortMirrorStore()
      await store.start()

      FakeWebSocket.instances[0].emit([mirror({ deployment_id: 'dep2', port: 5000 })])

      expect(store.mirrorsForDeployment('dep2')).toHaveLength(1)
    })

    it('keeps websocket open until all callers stop', async () => {
      const store = usePortMirrorStore()
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
      const store = usePortMirrorStore()
      await store.start()
      store.stop()
      expect(FakeWebSocket.instances[0].closed).toBe(true)
      expect(store.connected).toBe(false)
    })

    it('reconnects after stream closes while consumers are active', async () => {
      vi.useFakeTimers()
      const store = usePortMirrorStore()
      await store.start()
      FakeWebSocket.instances[0].onopen?.()

      FakeWebSocket.instances[0].serverClose()
      await vi.advanceTimersByTimeAsync(500)

      expect(FakeWebSocket.instances).toHaveLength(2)
      expect(api.listPortMirrors).toHaveBeenCalledTimes(2)
    })
  })

  describe('actions', () => {
    it('retry 调用 api.retryPortMirror 并透传 hostId/port', async () => {
      const store = usePortMirrorStore()
      await store.retry('h1', 3000)
      expect(api.retryPortMirror).toHaveBeenCalledWith('h1', 3000)
    })

    it('stopOccupier 调用 api.stopMirrorOccupier 并透传 hostId/port', async () => {
      const store = usePortMirrorStore()
      await store.stopOccupier('h1', 3000)
      expect(api.stopMirrorOccupier).toHaveBeenCalledWith('h1', 3000)
    })
  })
})
