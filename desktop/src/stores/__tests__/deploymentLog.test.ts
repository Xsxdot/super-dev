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
      fetchLogContextPage: vi.fn().mockResolvedValue({ items: [], has_more: false }),
    },
    deploymentWsUrl: actual.deploymentWsUrl,
  }
})

class MockWebSocket {
  static instances: MockWebSocket[] = []
  onopen: (() => void) | null = null
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

const trimBaseTime = Date.parse('2026-07-03T04:00:00.000Z')

function timestampFor(index: number): string {
  return new Date(trimBaseTime + index * 1000).toISOString()
}

function makeRawLog(index: number, deploymentId: string, extra: Partial<apiModule.LogEntry> = {}): apiModule.LogEntry {
  return {
    id: String(index),
    deployment_id: deploymentId,
    run_id: '',
    timestamp: timestampFor(index),
    level: 'INFO',
    message: `msg-${index}`,
    stream: 'stdout',
    ...extra,
  }
}

function sendWsLog(ws: MockWebSocket, raw: apiModule.LogEntry) {
  ws.onmessage?.({ data: JSON.stringify(raw) })
}

async function flushMicrotasks(times = 6) {
  for (let i = 0; i < times; i++) await Promise.resolve()
}

async function reconnectAndOpen(oldWs: MockWebSocket): Promise<MockWebSocket> {
  oldWs.close()
  await vi.advanceTimersByTimeAsync(1000)
  const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1]
  ws.onopen?.()
  return ws
}

describe('useDeploymentLogStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
  })

  it('subscribe 创建 WebSocket 连接', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    // subscribe 内部 connect() 现在是 async（要先经 Tauri IPC 拿本机 token 才能拼 WS URL），
    // 需要先把 pending 的 microtask 队列跑完，WebSocket 才真正被构造出来。
    await flushMicrotasks()
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.instances[0].url).toContain('dep-1')
  })

  it('connect() 等待本机 token 期间被 unsubscribe，不遗留孤儿 WebSocket', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-race')
    // 此刻 connect() 正卡在 await deploymentWsUrl(...)（本机 token 读取中的 microtask 链），
    // WebSocket 尚未构造；同步立即 unsubscribe，refCount 归零、session 被摘除。
    store.unsubscribe('dep-race')
    await flushMicrotasks()
    // connect() 恢复执行后应看到 session.refCount<=0 而放弃建连，不应留下一条没人管的 WS。
    expect(MockWebSocket.instances).toHaveLength(0)
  })

  it('subscribe 同一 deploymentId 两次，refCount 增加但只有一个 WS', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    store.subscribe('dep-1')
    await flushMicrotasks()
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(store.refCountOf('dep-1')).toBe(2)
  })

  it('unsubscribe 减少 refCount，归零时关闭 WS', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    store.subscribe('dep-1')
    // 必须等 WebSocket 真正建立后再 unsubscribe，否则 refCount 归零时 session 已被摘除，
    // connect() 对 await 之后的 session 校验会直接放弃建连，MockWebSocket 永远不会出现。
    await flushMicrotasks()
    store.unsubscribe('dep-1')
    expect(store.refCountOf('dep-1')).toBe(1)
    store.unsubscribe('dep-1')
    expect(store.refCountOf('dep-1')).toBe(0)
    expect(MockWebSocket.instances[0].readyState).toBe(3)
  })

  it('收到 WS 消息后日志出现在 getLogs', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    await flushMicrotasks()
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

  it('收到折叠增量时按 fold_key 更新已有行计数', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-1')
    await flushMicrotasks()
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

  it('不暴露测试专用写入 API 到生产 store surface', () => {
    const store = useDeploymentLogStore()
    expect('_ingestLive' in store).toBe(false)
    expect('_ingestHistory' in store).toBe(false)
    expect('_seedForTest' in store).toBe(false)
    expect('_trimHistoryHead' in store).toBe(false)
  })
})

describe('重连补拉', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
    vi.clearAllMocks()
  })

  it('lastSeen 游标优先取 seq，无 seq 时回退 rowid', async () => {
    const store = useDeploymentLogStore()
    const deploymentId = 'dep-last-seen-seq'
    store.subscribe(deploymentId)
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]

    sendWsLog(ws, makeRawLog(999, deploymentId, { seq: 42 }))
    expect(store.sessions.get(deploymentId)?.lastSeen).toEqual({
      time: timestampFor(999),
      id: '42',
    })

    sendWsLog(ws, makeRawLog(1000, deploymentId))
    expect(store.sessions.get(deploymentId)?.lastSeen).toEqual({
      time: timestampFor(1000),
      id: '1000',
    })
  })

  it('重连成功后按 lastSeen 游标向后补拉直到 hasMore=false', async () => {
    vi.useFakeTimers()
    try {
      const store = useDeploymentLogStore()
      const deploymentId = 'dep-catchup'
      store.subscribe(deploymentId, 'proj-1')
      await flushMicrotasks()
      const ws = MockWebSocket.instances[0]
      const first = makeRawLog(10, deploymentId, { seq: 42 })
      sendWsLog(ws, first)
      const mockFetch = vi.mocked(apiModule.api.fetchLogContextPage)
      mockFetch
        .mockResolvedValueOnce({
          deployment_id: deploymentId,
          direction: 'after',
          items: Array.from({ length: 200 }, (_, index) => makeRawLog(11 + index, deploymentId)),
          has_more: true,
        })
        .mockResolvedValueOnce({
          deployment_id: deploymentId,
          direction: 'after',
          items: Array.from({ length: 50 }, (_, index) => makeRawLog(211 + index, deploymentId)),
          has_more: false,
        })

      await reconnectAndOpen(ws)
      await flushMicrotasks()

      expect(mockFetch).toHaveBeenCalledWith(expect.objectContaining({
        project: 'proj-1',
        deployment: deploymentId,
        cursor_time: first.timestamp,
        cursor_id: '42',
        direction: 'after',
        limit: 200,
      }))
      const logs = store.getLogs(deploymentId)
      expect(logs).toHaveLength(251)
      expect(logs[0].message).toBe('msg-10')
      expect(logs[250].message).toBe('msg-260')
    } finally {
      vi.useRealTimers()
    }
  }, 30000)

  it('补拉期间到达的实时日志先缓冲、补拉完成后按序落入', async () => {
    vi.useFakeTimers()
    try {
      const store = useDeploymentLogStore()
      const deploymentId = 'dep-catchup-pending'
      store.subscribe(deploymentId, 'proj-1')
      await flushMicrotasks()
      const ws = MockWebSocket.instances[0]
      sendWsLog(ws, makeRawLog(10, deploymentId))
      let resolvePage!: (value: apiModule.LogContextPageResponse) => void
      vi.mocked(apiModule.api.fetchLogContextPage).mockReturnValueOnce(new Promise(resolve => {
        resolvePage = resolve
      }))

      const nextWs = await reconnectAndOpen(ws)
      sendWsLog(nextWs, makeRawLog(12, deploymentId))
      sendWsLog(nextWs, makeRawLog(13, deploymentId))
      sendWsLog(nextWs, makeRawLog(14, deploymentId))
      expect(store.getLogs(deploymentId).map(log => log.message)).toEqual(['msg-10'])

      resolvePage({
        deployment_id: deploymentId,
        direction: 'after',
        items: [makeRawLog(11, deploymentId)],
        has_more: false,
      })
      await flushMicrotasks()

      expect(store.getLogs(deploymentId).map(log => log.message)).toEqual([
        'msg-10',
        'msg-11',
        'msg-12',
        'msg-13',
        'msg-14',
      ])
    } finally {
      vi.useRealTimers()
    }
  }, 30000)

  it('补拉翻页达到上限仍 hasMore 时记录 gap marker', async () => {
    vi.useFakeTimers()
    try {
      const store = useDeploymentLogStore()
      const deploymentId = 'dep-catchup-capped'
      store.subscribe(deploymentId, 'proj-1')
      await flushMicrotasks()
      const ws = MockWebSocket.instances[0]
      sendWsLog(ws, makeRawLog(10, deploymentId))
      const mockFetch = vi.mocked(apiModule.api.fetchLogContextPage)
      for (let page = 0; page < 20; page++) {
        mockFetch.mockResolvedValueOnce({
          deployment_id: deploymentId,
          direction: 'after',
          items: [makeRawLog(11 + page, deploymentId)],
          has_more: true,
        })
      }

      await reconnectAndOpen(ws)
      await flushMicrotasks(30)

      expect(mockFetch).toHaveBeenCalledTimes(20)
      const markers = store.getGapMarkers(deploymentId)
      expect(markers).toHaveLength(1)
      expect(markers[0].time).toBe(timestampFor(30))
    } finally {
      vi.useRealTimers()
    }
  }, 30000)
})

describe('trim 与历史游标同步', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
    vi.clearAllMocks()
  })

  it('裁剪后 oldestCursor 前移到裁剪边界，hasMoreHistory 强制为 true', async () => {
    const store = useDeploymentLogStore()
    const deploymentId = 'dep-trim-cursor'
    store.subscribe(deploymentId)
    await flushMicrotasks()
    const session = store.sessions.get(deploymentId)!
    session.hasMoreHistory = false
    const ws = MockWebSocket.instances[0]

    for (let i = 1; i <= 5501; i++) {
      sendWsLog(ws, makeRawLog(i, deploymentId))
    }

    const logs = store.getLogs(deploymentId)
    const removedCount = 5501 - logs.length
    expect(logs.length).toBeLessThanOrEqual(5000)
    expect(session.oldestCursor).toEqual({
      time: timestampFor(removedCount),
      id: String(removedCount),
    })
    expect(session.hasMoreHistory).toBe(true)
  }, 30000)

  it('loadMoreHistory 在裁剪后携带 before 与 before_time', async () => {
    const store = useDeploymentLogStore()
    const deploymentId = 'dep-trim-before-time'
    store.subscribe(deploymentId)
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]
    for (let i = 1; i <= 5501; i++) {
      sendWsLog(ws, makeRawLog(i, deploymentId))
    }
    const session = store.sessions.get(deploymentId)!
    const cursor = session.oldestCursor!
    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce({ items: [] })

    await store.loadMoreHistory(deploymentId, 200)

    expect(mockFetch).toHaveBeenCalledWith(expect.objectContaining({
      deploymentId,
      limit: 200,
      before: cursor.id,
      beforeTime: cursor.time,
    }))
  }, 30000)

  it('trimmedFoldKeys 有 512 上限', async () => {
    const store = useDeploymentLogStore()
    const deploymentId = 'dep-trim-fold-cap'
    store.subscribe(deploymentId)
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]

    for (let i = 1; i <= 5501; i++) {
      sendWsLog(ws, makeRawLog(i, deploymentId, { fold_key: `fk-${i}`, repeat_count: i }))
    }

    const session = store.sessions.get(deploymentId)!
    expect(session.trimmedFoldKeys.size).toBeLessThanOrEqual(512)
  }, 30000)
})

describe('log ingestion', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    MockWebSocket.instances = []
    vi.clearAllMocks()
  })

  it('inserts logs in sorted order by timestamp+id', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]

    // 乱序发送，期望按 id/timestamp 排序
    ws.onmessage?.({ data: JSON.stringify({ id: '3', timestamp: '2024-01-01T00:00:03Z', message: 'c', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '2', timestamp: '2024-01-01T00:00:02Z', message: 'b', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })

    const logs = store.getLogs('dep1')
    expect(logs.map(l => l.id)).toEqual(['x:1', 'x:2', 'x:3'])
  })

  it('deduplicates by id', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: '2024-01-01T00:00:01Z', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })

    expect(store.getLogs('dep1')).toHaveLength(1)
  })

  it('timestamp 无效时退回按 id 稳定排序', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-invalid-time')
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({ id: '1', timestamp: 'invalid-time', message: 'a', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '2', timestamp: 'invalid-time', message: 'b', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '3', timestamp: 'invalid-time', message: 'c', level: 'info', source_id: 'x', deployment_id: '', run_id: '', stream: '' }) })

    expect(store.getLogs('dep-invalid-time').map(l => l.id)).toEqual(['x:1', 'x:2', 'x:3'])
  })

  it('历史区超出 MAX_LOGS 时截断到不超过 MAX_LOGS 条', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-trim')

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce({
      items: Array.from({ length: 5001 }, (_, index) => {
        const i = index + 1
        return {
        id: String(i),
        timestamp: `2024-01-01T00:00:${String(i).padStart(5, '0')}Z`,
        message: `msg-${i}`,
        level: 'INFO',
        deployment_id: '',
        run_id: '',
        stream: 'stdout',
        }
      }),
    })

    await store.loadMoreHistory('dep-trim', 5001)

    expect(store.getLogs('dep-trim').length).toBeLessThanOrEqual(5000)
  }, 30000)

  it('实时区超出 MAX_LOGS 时也会裁剪，避免 live-only 会话无限增长', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-live-trim')
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]

    for (let i = 1; i <= 5001; i++) {
      ws.onmessage?.({ data: JSON.stringify({
        id: '0',
        deployment_id: 'dep-live-trim',
        run_id: '',
        timestamp: `2024-01-01T00:00:${String(i).padStart(5, '0')}Z`,
        level: 'INFO',
        message: `live-${i}`,
        stream: 'stdout',
      }) })
    }

    expect(store.getLogs('dep-live-trim').length).toBeLessThanOrEqual(5000)
  }, 30000)

  it('重复的无 rowid 实时日志不互相覆盖', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-live-duplicate')
    await flushMicrotasks()
    const ws = MockWebSocket.instances[0]
    const raw = {
      id: '0',
      deployment_id: 'dep-live-duplicate',
      run_id: '',
      timestamp: '2024-01-01T00:00:01Z',
      level: 'INFO',
      message: 'same payload',
      stream: 'stdout',
    }

    ws.onmessage?.({ data: JSON.stringify(raw) })
    ws.onmessage?.({ data: JSON.stringify(raw) })

    expect(store.getLogs('dep-live-duplicate')).toHaveLength(2)
  })

  it('实时日志走 appendLive 追加，窗口内乱序不插到历史区上方', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-live-partition')
    await flushMicrotasks()
    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce({
      items: [{ id: '10', deployment_id: 'dep-live-partition', run_id: '', timestamp: '2024-01-01T00:00:00Z', level: 'INFO', message: 'history', stream: 'stdout' }],
    })
    await store.loadMoreHistory('dep-live-partition', 1)
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({ id: '0', deployment_id: 'dep-live-partition', run_id: '', timestamp: '2024-01-01T00:00:02Z', level: 'INFO', message: 'b', stream: 'stdout' }) })
    ws.onmessage?.({ data: JSON.stringify({ id: '0', deployment_id: 'dep-live-partition', run_id: '', timestamp: '2024-01-01T00:00:01Z', level: 'INFO', message: 'a', stream: 'stdout' }) })

    expect(store.getLogs('dep-live-partition').map(log => log.message)).toEqual(['history', 'a', 'b'])
  })

  it('裁剪只裁历史头部，保留 fold 映射用于增量 miss 判定', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-fold-trim')
    await flushMicrotasks()
    const events: string[] = []
    window.addEventListener('superdev:log-panel', (event) => {
      events.push((event as CustomEvent).detail.event)
    })
    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce({
      items: Array.from({ length: 5001 }, (_, index) => {
        const i = index + 1
        return {
          id: String(i),
          deployment_id: 'dep-fold-trim',
          run_id: '',
          timestamp: `2024-01-01T00:00:${String(i).padStart(5, '0')}Z`,
          level: 'INFO',
          message: `msg-${i}`,
          stream: 'stdout',
          repeat_count: i === 1 ? 5 : 1,
          fold_key: i === 1 ? 'fk-1' : undefined,
        }
      }),
    })
    await store.loadMoreHistory('dep-fold-trim', 5001)
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({
      id: '0',
      deployment_id: 'dep-fold-trim',
      run_id: '',
      timestamp: '',
      level: '',
      message: '',
      stream: '',
      repeat_count: 6,
      fold_key: 'fk-1',
    }) })

    expect(events).not.toContain('log_store.increment_miss')
  }, 30000)
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
    await flushMicrotasks()
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
