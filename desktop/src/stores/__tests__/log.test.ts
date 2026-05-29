/**
 * logStore 测试服务日志订阅时的历史恢复。
 *
 * 职责：
 *   - 验证订阅服务时先拉取最近历史日志
 *   - 验证 REST 历史和 WebSocket recent 重复时不会重复显示
 *
 * 边界：
 *   - 不建立真实 WebSocket
 *   - 不挂载日志面板组件
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api as agentApi, type LogEntry } from '@/api/agent'
import { useLogStore } from '../log'

class MockWebSocket {
  static OPEN = 1
  static CLOSED = 3
  static instances: MockWebSocket[] = []
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  readyState = MockWebSocket.OPEN
  url: string

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }

  emit(entry: LogEntry) {
    this.onmessage?.({ data: JSON.stringify(entry) })
  }
}

function log(id: number, message: string): LogEntry {
  return {
    id,
    service_id: 'svc-api',
    run_id: 'run-1',
    timestamp: `2026-05-21T10:00:0${id}.000Z`,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('logStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  it('subscribe 先加载最近历史日志并记录历史边界', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([log(1, 'history 1'), log(2, 'history 2')])
    const store = useLogStore()

    await store.subscribe('svc-api')

    expect(agentApi.fetchLogs).toHaveBeenCalledWith({ service: 'svc-api', limit: 200 })
    expect(store.getLogs('svc-api').map(l => l.message)).toEqual(['history 1', 'history 2'])
    expect(store.getHistoryBoundary('svc-api')).toEqual({ timestamp: '2026-05-21T10:00:02.000Z', id: 2 })
  })

  it('WebSocket recent 与历史接口重复时按日志签名去重', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([log(1, 'history 1')])
    const store = useLogStore()

    await store.subscribe('svc-api')
    MockWebSocket.instances[0].emit(log(1, 'history 1'))
    MockWebSocket.instances[0].emit(log(2, 'live 2'))

    expect(store.getLogs('svc-api').map(l => l.message)).toEqual(['history 1', 'live 2'])
  })
})

describe('log trimming', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  it('超出 MAX_LOGS 时批量截断并同步清理 seenSignatures', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([])
    const store = useLogStore()
    await store.subscribe('svc-trim')
    const ws = MockWebSocket.instances[0]

    // 注入 5001 条不同日志
    for (let i = 1; i <= 5001; i++) {
      ws.onmessage?.({ data: JSON.stringify(log(i, `msg-${i}`)) })
    }

    const logs = store.getLogs('svc-trim')
    // 截断后不超过 MAX_LOGS (5000)
    expect(logs.length).toBeLessThanOrEqual(5000)
    // seenSignatures 不应无限增长——其大小应与 logs 数量接近（允许少量偏差）
    const entry = (store as any).serviceLogs['svc-trim']
    expect(entry.seenSignatures.size).toBeLessThanOrEqual(5000 + 10)
  })

  it('折叠行被截断时也能正确清理 seenSignatures', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([])
    const store = useLogStore()
    await store.subscribe('svc-fold')
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1]

    // 注入 11000 条日志，其中每隔一条是重复（触发 fold）。
    // 每 2 条折叠成 1 个 log 条目，约 5500 条，超过 MAX_LOGS=5000 触发 trim。
    for (let i = 1; i <= 11000; i++) {
      // 奇数 id 用唯一消息，偶数 id 重复上一条消息（触发 fold）
      const message = i % 2 === 0 ? `msg-${i - 1}` : `msg-${i}`
      ws.onmessage?.({ data: JSON.stringify(log(i, message)) })
    }

    const logs = store.getLogs('svc-fold')
    expect(logs.length).toBeLessThanOrEqual(5000)
    const entry = (store as any).serviceLogs['svc-fold']
    // seenSignatures 不应无限增长——最多与 MAX_LOGS 同数量级（含折叠签名约 2x）
    expect(entry.seenSignatures.size).toBeLessThanOrEqual(5000 * 2 + 100)
  })
})
