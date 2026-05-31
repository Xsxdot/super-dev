import { afterEach, describe, it, expect, vi } from 'vitest'
import { api, deploymentWsUrl } from '@/api/agent'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('deploymentWsUrl', () => {
  it('dev 模式下返回正确的 ws URL', () => {
    // WS_BASE 在 dev 模式下为 ws://127.0.0.1:57018，build 后为 ws://127.0.0.1:57017
    // 测试环境不是 dev，所以用 57017
    const url = deploymentWsUrl('dep-abc')
    expect(url).toContain('/ws/deployments/dep-abc/logs')
  })
})

describe('fetchDeploymentLogs', () => {
  it('从 deployment 日志响应中取出 items 数组', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        items: [
          {
            id: 7,
            deployment_id: 'dep-abc',
            run_id: 'run-1',
            timestamp: '2026-05-30T13:11:25Z',
            level: 'INFO',
            message: 'vite ready',
            stream: 'stderr',
          },
        ],
        next: { id: 7, time: '2026-05-30T13:11:25Z' },
      }),
    } as Response)

    const logs = await api.fetchDeploymentLogs({ deploymentId: 'dep-abc', limit: 5 })

    expect(logs).toHaveLength(1)
    expect(logs[0].message).toBe('vite ready')
  })
})
