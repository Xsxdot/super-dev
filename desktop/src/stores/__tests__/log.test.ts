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
