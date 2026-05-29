/**
 * deploymentLog store 测试 deployment 日志订阅的 refCount 和 WebSocket 行为。
 *
 * 职责：
 *   - 验证 subscribe/unsubscribe refCount 逻辑
 *   - 验证 WebSocket 消息被正确解析并写入 getLogs
 *   - 验证 insertSorted 有序插入与去重
 *   - 验证 loadMoreHistory 传递正确的 before 游标
 *
 * 边界：
 *   - 不建立真实 WebSocket 或 HTTP 连接
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect, vi } from 'vitest'
import { useDeploymentLogStore } from '../deploymentLog'
import * as apiModule from '@/api/agent'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof apiModule>()
  return {
    ...actual,
    api: {
      ...actual.api,
      fetchDeploymentLogs: vi.fn().mockResolvedValue([]),
    },
    deploymentWsUrl: actual.deploymentWsUrl,
  }
})

class MockWebSocket {
  static instances: MockWebSocket[] = []
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  readyState = 1
  constructor(public url: string) {
    MockWebSocket.instances.push(this)
  }
  close() { this.readyState = 3; this.onclose?.() }
  send(_data: string) {}
}

vi.stubGlobal('WebSocket', MockWebSocket)

describe('useDeploymentLogStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
  })

  it('subscribe 创建 WebSocket 连接', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.instances[0].url).toContain('dep-1')
  })

  it('subscribe 同一 deploymentId 两次，refCount 增加但只有一个 WS', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    store.subscribe('dep-1')
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(store.refCountOf('dep-1')).toBe(2)
  })

  it('unsubscribe 减少 refCount，归零时关闭 WS', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    store.subscribe('dep-1')
    store.unsubscribe('dep-1')
    expect(store.refCountOf('dep-1')).toBe(1)
    store.unsubscribe('dep-1')
    expect(store.refCountOf('dep-1')).toBe(0)
    expect(MockWebSocket.instances[0].readyState).toBe(3)
  })

  it('收到 WS 消息后日志出现在 getLogs', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    const ws = MockWebSocket.instances[0]
    ws.onmessage?.({ data: JSON.stringify({
      id: 1,
      service_id: 'svc',
      run_id: 'r',
      timestamp: '2024-01-01T00:00:00Z',
      level: 'INFO',
      message: 'hello',
      stream: 'stdout'
    }) })
    const logs = store.getLogs('dep-1')
    expect(logs).toHaveLength(1)
    expect(logs[0].message).toBe('hello')
  })

  it('getLogs 未知 deploymentId 返回空数组', () => {
    const store = useDeploymentLogStore()
    expect(store.getLogs('unknown')).toEqual([])
  })

  it('refCountOf 未知 deploymentId 返回 0', () => {
    const store = useDeploymentLogStore()
    expect(store.refCountOf('unknown')).toBe(0)
  })
})

describe('log ingestion', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
  })

  it('inserts logs in sorted order by timestamp+id', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    const ws = MockWebSocket.instances[0]

    // 乱序发送，期望按 id/timestamp 排序
    ws.onmessage?.({ data: JSON.stringify({ id: 3, timestamp: '2024-01-01T00:00:03Z', message: 'c', level: 'info', source_id: 'x', service_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: 1, timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', service_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: 2, timestamp: '2024-01-01T00:00:02Z', message: 'b', level: 'info', source_id: 'x', service_id: '', run_id: '', stream: '' }) })

    const logs = store.getLogs('dep1')
    expect(logs.map(l => l.id)).toEqual([1, 2, 3])
  })

  it('deduplicates by id', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({ id: 1, timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', service_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: 1, timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', service_id: '', run_id: '', stream: '' }) })

    expect(store.getLogs('dep1')).toHaveLength(1)
  })

  it('超出 MAX_LOGS 时截断到不超过 MAX_LOGS 条', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-trim')
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1]

    // 注入 5001 条日志
    for (let i = 1; i <= 5001; i++) {
      ws.onmessage?.({ data: JSON.stringify({
        id: i,
        timestamp: `2024-01-01T00:00:${String(i).padStart(5, '0')}Z`,
        message: `msg-${i}`,
        level: 'INFO',
        service_id: '',
        run_id: '',
        stream: 'stdout',
      }) })
    }

    expect(store.getLogs('dep-trim').length).toBeLessThanOrEqual(5000)
  }, 30000)
})

describe('loadMoreHistory', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
  })

  it('passes oldestLoadedId as before cursor', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    const ws = MockWebSocket.instances[0]

    // 先通过 WS 注入一条日志，建立 oldestLoadedId = 5
    ws.onmessage?.({ data: JSON.stringify({ id: 5, timestamp: '2024-01-01T00:00:05Z', message: 'e', level: 'info', source_id: 'x', service_id: '', run_id: '', stream: '' }) })

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce([
      { id: 3, timestamp: '2024-01-01T00:00:03Z', message: 'c', level: 'info', service_id: '', run_id: '', stream: '' },
      { id: 2, timestamp: '2024-01-01T00:00:02Z', message: 'b', level: 'info', service_id: '', run_id: '', stream: '' },
    ])

    await store.loadMoreHistory('dep1', 200)

    expect(mockFetch).toHaveBeenCalledWith(expect.objectContaining({ before: 5 }))
  })
})
