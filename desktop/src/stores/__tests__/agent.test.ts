/**
 * agentStore 生命周期操作测试
 *
 * 职责：
 *   - 验证 start/stop/restart 成功后记录当前会话内的日志分割 marker
 *
 * 边界：
 *   - 不建立真实 HTTP 连接，API 层通过 mock 验证
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore } from '../agent'
import { useLogLifecycleStore } from '../logLifecycle'
import { api } from '@/api/agent'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      startDeployment: vi.fn().mockResolvedValue(undefined),
      stopDeployment: vi.fn().mockResolvedValue(undefined),
      restartDeployment: vi.fn().mockResolvedValue(undefined),
    },
  }
})

describe('agent deployment lifecycle markers', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('records lifecycle markers after successful deployment actions', async () => {
    const agent = useAgentStore()
    const lifecycle = useLogLifecycleStore()

    await agent.startDeployment('dep-1')
    await agent.stopDeployment('dep-1')
    await agent.restartDeployment('dep-1')

    expect(lifecycle.getMarkers('dep-1').map(m => m.kind)).toEqual(['start', 'stop', 'restart'])
  })

  it('does not record a marker when the API call fails', async () => {
    vi.mocked(api.startDeployment).mockRejectedValueOnce(new Error('boom'))
    const agent = useAgentStore()
    const lifecycle = useLogLifecycleStore()

    await expect(agent.startDeployment('dep-1')).rejects.toThrow('boom')

    expect(lifecycle.getMarkers('dep-1')).toEqual([])
  })
})
