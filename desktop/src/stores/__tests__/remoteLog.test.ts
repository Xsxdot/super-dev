/**
 * remoteLog store 测试多节点远程日志订阅。
 *
 * 职责：
 *   - 验证按分组为每个 Host 建立隧道与 WebSocket
 *   - 验证多 Host 日志按 timestamp 归并
 *   - 验证引用计数、失败隔离和历史拉取
 *
 * 边界：
 *   - 不建立真实 WebSocket 或 HTTP 连接
 */
import { describe, it, expect, beforeEach, vi, type Mock } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useRemoteLogStore } from '@/stores/remoteLog'
import { api } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      openTunnel: vi.fn((hostId: string) =>
        Promise.resolve({
          host_id: hostId,
          state: 'open',
          local_port: hostId === 'h1' ? 57100 : 57101,
        }),
      ),
      closeTunnel: vi.fn().mockResolvedValue(undefined),
      getRemoteView: vi.fn().mockResolvedValue({
        log_source: {
          id: 'ls1',
          name: 'nova-api',
          type: 'journalctl',
          host_ids: ['h1', 'h2'],
          created_at: '',
          updated_at: '',
        },
        groups: [
          { group_key: 'all', host_ids: ['h1', 'h2'] },
          { group_key: 'prod', host_ids: ['h1', 'h2'] },
        ],
        hosts: [
          {
            id: 'h1',
            name: 'host-01',
            ssh_host: '',
            ssh_port: 22,
            ssh_user: '',
            remote_agent_port: 57017,
            tags: ['prod'],
            created_at: '',
            updated_at: '',
          },
          {
            id: 'h2',
            name: 'host-02',
            ssh_host: '',
            ssh_port: 22,
            ssh_user: '',
            remote_agent_port: 57017,
            tags: ['prod'],
            created_at: '',
            updated_at: '',
          },
        ],
      }),
    },
    WS_BASE: 'ws://127.0.0.1:57018',
  }
})

class MockWebSocket {
  static instances: MockWebSocket[] = []
  static OPEN = 1

  url: string
  readyState = 0
  onopen: ((event?: unknown) => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onerror: ((event?: unknown) => void) | null = null
  onclose: ((event?: unknown) => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
    setTimeout(() => {
      this.readyState = MockWebSocket.OPEN
      this.onopen?.()
    }, 0)
  }

  send() {}

  close() {
    this.closed = true
    this.readyState = 3
    this.onclose?.()
  }

  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

function log(id: number, message: string, timestamp: string) {
  return {
    id,
    service_id: 's',
    run_id: 'r',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  MockWebSocket.instances = []
  ;(globalThis as unknown as { WebSocket: typeof MockWebSocket }).WebSocket = MockWebSocket
  vi.clearAllMocks()
  ;(api.openTunnel as Mock).mockImplementation((hostId: string) =>
    Promise.resolve({
      host_id: hostId,
      state: 'open',
      local_port: hostId === 'h1' ? 57100 : 57101,
    }),
  )
})

describe('useRemoteLogStore', () => {
  it('subscribe 为 group 内每个 host 建立隧道与 WS', async () => {
    const store = useRemoteLogStore()

    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))

    expect((api.openTunnel as Mock).mock.calls.map(call => call[0]).sort()).toEqual(['h1', 'h2'])
    expect(MockWebSocket.instances).toHaveLength(2)
  })

  it('多 host WS 消息按时间戳归并', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))
    const [ws1, ws2] = MockWebSocket.instances

    ws2.emit(log(100, 'B', '2026-05-21T12:00:01Z'))
    ws1.emit(log(99, 'A', '2026-05-21T12:00:00Z'))
    ws2.emit(log(101, 'C', '2026-05-21T12:00:02Z'))

    const logs = store.logsOf('ls1', 'all')
    expect(logs.map(entry => entry.message)).toEqual(['A', 'B', 'C'])
    expect(logs.map(entry => entry.host_id)).toEqual(['h1', 'h2', 'h2'])
  })

  it('unsubscribe 关闭所有 WS', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))

    store.unsubscribe('ls1', 'all')

    for (const ws of MockWebSocket.instances) expect(ws.closed).toBe(true)
  })

  it('参与同一 group 的重复 subscribe 共享连接', async () => {
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))
    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))

    expect(MockWebSocket.instances).toHaveLength(2)
    store.unsubscribe('ls1', 'all')
    expect(MockWebSocket.instances.every(ws => !ws.closed)).toBe(true)
    store.unsubscribe('ls1', 'all')
    expect(MockWebSocket.instances.every(ws => ws.closed)).toBe(true)
  })

  it('host 隧道失败标记错误，其他 host 不受影响', async () => {
    ;(api.openTunnel as Mock).mockImplementation((hostId: string) => {
      if (hostId === 'h1') return Promise.reject(new Error('connect refused'))
      return Promise.resolve({ host_id: 'h2', state: 'open', local_port: 57101 })
    })
    const store = useRemoteLogStore()

    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))

    expect(store.errorOf('ls1', 'all', 'h1')).toContain('connect refused')
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('loadHistory 从每个 host 的 tunnel 拉取历史并归并', async () => {
    const fetchMock = vi.fn((url: string) => {
      const items = url.includes('57100')
        ? [log(1, 'old-A', '2026-05-21T11:59:59Z')]
        : [log(2, 'old-B', '2026-05-21T12:00:00Z')]
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(items),
      } as Response)
    })
    ;(globalThis as unknown as { fetch: typeof fetchMock }).fetch = fetchMock
    const store = useRemoteLogStore()
    await store.subscribe('ls1', 'all')
    await new Promise(resolve => setTimeout(resolve, 5))

    await store.loadHistory('ls1', 'all')

    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(store.logsOf('ls1', 'all').map(entry => entry.message)).toEqual(['old-A', 'old-B'])
  })
})
