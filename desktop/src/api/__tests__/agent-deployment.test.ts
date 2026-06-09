import { afterEach, describe, it, expect, vi } from 'vitest'
import { api, deploymentWsUrl, isApprovalRequiredError } from '@/api/agent'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
  vi.unstubAllEnvs()
})

describe('deploymentWsUrl', () => {
  it('dev 模式下返回正确的 ws URL', () => {
    // WS_BASE 在 dev 模式下为 ws://127.0.0.1:57018，build 后为 ws://127.0.0.1:57017
    // 测试环境不是 dev，所以用 57017
    const url = deploymentWsUrl('dep-abc')
    expect(url).toContain('/ws/deployments/dep-abc/logs')
  })

  it('允许通过 VITE_AGENT_HOST 覆盖本地 agent 地址', async () => {
    vi.stubEnv('VITE_AGENT_HOST', '127.0.0.1:57118')
    vi.resetModules()

    const agentModule = await import('@/api/agent')

    expect(agentModule.AGENT_HOST).toBe('127.0.0.1:57118')
    expect(agentModule.deploymentWsUrl('dep-abc')).toContain('ws://127.0.0.1:57118')
  })
})

describe('fetchDeploymentLogs', () => {
  it('从 deployment 日志响应中取出 items 数组', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        items: [
          {
            id: '7',
            deployment_id: 'dep-abc',
            run_id: 'run-1',
            timestamp: '2026-05-30T13:11:25Z',
            level: 'INFO',
            message: 'vite ready',
            stream: 'stderr',
          },
        ],
        next: { id: '7', time: '2026-05-30T13:11:25Z' },
      }),
    } as Response)

    const logs = await api.fetchDeploymentLogs({ deploymentId: 'dep-abc', limit: 5 })

    expect(logs).toHaveLength(1)
    expect(logs[0].id).toBe('7')
    expect(logs[0].message).toBe('vite ready')
  })
})

describe('pipeline template api', () => {
  it('listPipelineTemplates returns items', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ items: [{ source: 'builtin', id: 'go-binary-build', version: '1.0.0', digest: 'sha256:x' }] }),
    } as Response)

    const res = await api.listPipelineTemplates()

    expect(res.items[0].id).toBe('go-binary-build')
    expect(globalThis.fetch).toHaveBeenCalledWith(expect.stringContaining('/api/pipeline/templates'), expect.any(Object))
  })
})

describe('operation approval api', () => {
  it('loads approvals and approves one request', async () => {
    globalThis.fetch = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve([
          { id: 'opa_1', status: 'pending', plan: { id: 'op_1', kind: 'runtime.restart', risk_level: 'high', target: {}, fingerprint: 'fp_1' } },
        ]),
      } as Response)
      .mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          id: 'opa_1',
          status: 'approved',
        }),
      } as Response)

    const approvals = await api.listOperationApprovals({ status: 'pending' })
    expect(approvals[0].id).toBe('opa_1')

    await api.approveOperationApproval('opa_1', { decided_by: 'user', note: 'ok' })
    expect(globalThis.fetch).toHaveBeenLastCalledWith(expect.stringContaining('/api/operation-approvals/opa_1/approve'), expect.any(Object))
  })

  it('preserves structured approval_required errors', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      statusText: 'Forbidden',
      json: () => Promise.resolve({
        code: 'approval_required',
        error: 'approval required',
        plan: {
          id: 'op_1',
          kind: 'runtime.start',
          target: { deployment_id: 'dep-prod' },
          risk_level: 'high',
          requires_approval: true,
          denied: false,
          fingerprint: 'fp_1',
        },
        approval: {
          id: 'opa_1',
          status: 'pending',
          requested_by: 'desktop',
          plan: {
            id: 'op_1',
            kind: 'runtime.start',
            target: { deployment_id: 'dep-prod' },
            risk_level: 'high',
            requires_approval: true,
            denied: false,
            fingerprint: 'fp_1',
          },
        },
      }),
    } as Response)

    let caught: unknown
    try {
      await api.startDeployment('dep-prod')
    } catch (error) {
      caught = error
    }

    expect(caught).toMatchObject({
      code: 'approval_required',
      approval: expect.objectContaining({ id: 'opa_1' }),
    })
    expect(isApprovalRequiredError(caught)).toBe(true)
  })

  it('marks desktop requests with requester headers', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ status: 'starting' }),
    } as Response)

    await api.startDeployment('dep-dev')

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/deployments/dep-dev/start'),
      expect.objectContaining({
        headers: expect.objectContaining({
          'X-SuperDev-Requester': 'desktop',
          'X-SuperDev-Requester-Label': 'SuperDev Desktop',
        }),
      }),
    )
  })
})
