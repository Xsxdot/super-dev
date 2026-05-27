/**
 * deploymentLog store 测试 deployment 日志订阅的 refCount 和 WebSocket 行为。
 *
 * 职责：
 *   - 验证 subscribe/unsubscribe refCount 逻辑
 *   - 验证 WebSocket 消息被正确解析并写入 getLogs
 *
 * 边界：
 *   - 不建立真实 WebSocket 或 HTTP 连接
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect, vi } from 'vitest'
import { useDeploymentLogStore } from '../deploymentLog'

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
