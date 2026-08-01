import { beforeEach, describe, expect, it, vi } from 'vitest'

const invokeMock = vi.fn()
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invokeMock(...args) }))

describe('agentAuth', () => {
  beforeEach(() => {
    vi.resetModules()
    invokeMock.mockReset()
  })

  it('缓存 Tauri 命令返回的 token，重复调用不重复 invoke', async () => {
    invokeMock.mockResolvedValue('tok-1')
    const { agentToken } = await import('../agentAuth')
    expect(await agentToken()).toBe('tok-1')
    expect(await agentToken()).toBe('tok-1')
    expect(invokeMock).toHaveBeenCalledTimes(1)
  })

  it('invalidate 后重新 invoke（覆盖 agent 重启轮换）', async () => {
    invokeMock.mockResolvedValueOnce('tok-1').mockResolvedValueOnce('tok-2')
    const { agentToken, invalidateAgentToken } = await import('../agentAuth')
    expect(await agentToken()).toBe('tok-1')
    invalidateAgentToken()
    expect(await agentToken()).toBe('tok-2')
  })

  it('非 Tauri 环境（invoke 抛错）回落 null，不抛出', async () => {
    invokeMock.mockRejectedValue(new Error('not in tauri'))
    const { agentToken } = await import('../agentAuth')
    expect(await agentToken()).toBeNull()
  })

  it('withWsToken 追加 access_token，已有 query 用 & 连接，无 token 原样返回', async () => {
    invokeMock.mockResolvedValue('tok-1')
    const { withWsToken } = await import('../agentAuth')
    expect(await withWsToken('ws://x/ws/nodes')).toBe('ws://x/ws/nodes?access_token=tok-1')
    expect(await withWsToken('ws://x/ws/logs?run=1')).toBe('ws://x/ws/logs?run=1&access_token=tok-1')
  })
})
