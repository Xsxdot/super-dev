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
      fetchDeploymentLogs: vi.fn().mockResolvedValue({ items: [] }),
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
  url: string
  constructor(url: string) {
    this.url = url
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
      id: '1',
      deployment_id: 'svc',
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

  it('收到折叠增量时按 fold_key 更新已有行计数', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({
      id: '1',
      deployment_id: 'dep-1',
      run_id: 'r',
      timestamp: '2024-01-01T00:00:00Z',
      level: 'INFO',
      message: 'boom',
      stream: 'stdout',
      repeat_count: 1,
      fold_key: 'k1',
    }) })
    ws.onmessage?.({ data: JSON.stringify({
      id: '0',
      deployment_id: 'dep-1',
      run_id: '',
      timestamp: '',
      level: '',
      message: '',
      stream: '',
      repeat_count: 4,
      fold_key: 'k1',
    }) })

    const logs = store.getLogs('dep-1')
    expect(logs).toHaveLength(1)
    expect(logs[0].repeat_count).toBe(4)
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
    ws.onmessage?.({ data: JSON.stringify({ id: '3', timestamp: '2024-01-01T00:00:03Z', message: 'c', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '2', timestamp: '2024-01-01T00:00:02Z', message: 'b', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })

    const logs = store.getLogs('dep1')
    expect(logs.map(l => l.id)).toEqual(['x:1', 'x:2', 'x:3'])
  })

  it('deduplicates by id', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })

    expect(store.getLogs('dep1')).toHaveLength(1)
  })

  it('timestamp 无效时退回按 id 稳定排序', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-invalid-time')
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: 'invalid-time', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '2', timestamp: 'invalid-time', message: 'b', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '3', timestamp: 'invalid-time', message: 'c', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })

    expect(store.getLogs('dep-invalid-time').map(l => l.id)).toEqual(['x:1', 'x:2', 'x:3'])
  })

  it('历史区超出 MAX_LOGS 时截断到不超过 MAX_LOGS 条', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-trim')

    // 历史裁剪只移除历史头部，实时尾部由 Task 4 的分区语义保留。
    for (let i = 1; i <= 5001; i++) {
      store._ingestHistory('dep-trim', {
        id: String(i),
        timestamp: `2024-01-01T00:00:${String(i).padStart(5, '0')}Z`,
        message: `msg-${i}`,
        level: 'INFO',
        deployment_id: '',
        run_id: '',
        stream: 'stdout',
      } as any)
    }

    expect(store.getLogs('dep-trim').length).toBeLessThanOrEqual(5000)
  }, 30000)

  it('实时日志走 appendLive 追加，窗口内乱序不插到历史区上方', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-live-partition')

    store._ingestHistory('dep-live-partition', { id: '10', deployment_id: 'dep-live-partition', run_id: '', timestamp: '2024-01-01T00:00:00Z', level: 'INFO', message: 'history', stream: 'stdout' } as any)
    store._ingestLive('dep-live-partition', { id: '0', deployment_id: 'dep-live-partition', run_id: '', timestamp: '2024-01-01T00:00:02Z', level: 'INFO', message: 'b', stream: 'stdout' } as any)
    store._ingestLive('dep-live-partition', { id: '0', deployment_id: 'dep-live-partition', run_id: '', timestamp: '2024-01-01T00:00:01Z', level: 'INFO', message: 'a', stream: 'stdout' } as any)

    expect(store.getLogs('dep-live-partition').map(log => log.message)).toEqual(['history', 'a', 'b'])
  })

  it('裁剪只裁历史头部，保留 fold 映射', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-fold-trim')

    store._seedForTest('dep-fold-trim', { foldKey: 'fk-1', repeatCount: 5 })
    store._trimHistoryHead('dep-fold-trim', 1)

    expect(store._trimmedFoldKeysForTest('dep-fold-trim').get('fk-1')).toBe(5)
  })
})

describe('loadMoreHistory', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
    vi.clearAllMocks()
  })

  it('uses the oldest history id as before cursor for subsequent pages', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch
      .mockResolvedValueOnce({
        items: [
          { id: '5', timestamp: '2024-01-01T00:00:05Z', message: 'e', level: 'info', deployment_id: 'dep1', run_id: '', stream: '' },
          { id: '6', timestamp: '2024-01-01T00:00:06Z', message: 'f', level: 'info', deployment_id: 'dep1', run_id: '', stream: '' },
        ],
        next: { id: 'cursor-5', time: '2024-01-01T00:00:05Z' },
      })
      .mockResolvedValueOnce({
        items: [
          { id: '3', timestamp: '2024-01-01T00:00:03Z', message: 'c', level: 'info', deployment_id: 'dep1', run_id: '', stream: '' },
        ],
        next: { id: 'cursor-3', time: '2024-01-01T00:00:03Z' },
      })

    await store.loadMoreHistory('dep1', 2)
    await store.loadMoreHistory('dep1', 2)

    expect(mockFetch).toHaveBeenNthCalledWith(1, expect.objectContaining({
      deploymentId: 'dep1',
      limit: 2,
      before: undefined,
    }))
    expect(mockFetch).toHaveBeenNthCalledWith(2, expect.objectContaining({
      deploymentId: 'dep1',
      limit: 2,
      before: 'cursor-5',
    }))
  })

  it('does not let websocket ids advance the history pagination cursor', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({
      id: '1',
      timestamp: '2024-01-01T00:00:10Z',
      message: 'live',
      level: 'INFO',
      deployment_id: 'dep1',
      run_id: '',
      stream: 'stdout',
    }) })

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce({
      items: [
        { id: '20', timestamp: '2024-01-01T00:00:05Z', message: 'history', level: 'INFO', deployment_id: 'dep1', run_id: '', stream: 'stdout' },
      ],
      next: { id: 'cursor-20', time: '2024-01-01T00:00:05Z' },
    })

    await store.loadMoreHistory('dep1', 200)

    expect(mockFetch).toHaveBeenCalledWith(expect.objectContaining({ before: undefined }))
  })

  it('keeps history rows with the same remote sqlite id from different sources', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce({
      items: [
        { id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'from h1', level: 'INFO', deployment_id: 'dep1', run_id: '', stream: 'stdout', source_id: 'h1' },
        { id: '1', timestamp: '2024-01-01T00:00:02Z', message: 'from h2', level: 'INFO', deployment_id: 'dep1', run_id: '', stream: 'stdout', source_id: 'h2' },
      ],
      next: { id: 'cursor-1', time: '2024-01-01T00:00:01Z' },
    })

    await store.loadMoreHistory('dep1', 2)

    expect(store.getLogs('dep1').map(log => log.message)).toEqual(['from h1', 'from h2'])
  })
})
