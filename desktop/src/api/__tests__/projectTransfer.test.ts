/**
 * Project home transfer API contract tests.
 *
 * Responsibilities:
 *   - Verify transferPreflight/startTransfer/transferStatus/transferBack build the
 *     exact request path, method, and body the Go agent endpoints (Task 4/5/9) expect
 *   - Verify deleteHost surfaces the stable `project_home` error code (409) so callers
 *     (Task 12) can branch on it
 *   - Verify updateHost's response type carries the optional homed_projects field
 *
 * Boundaries:
 *   - Does not call a real Agent process
 *   - Does not test Desktop UI rendering (Task 11/12)
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/agent'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('transferPreflight', () => {
  it('sends host_id and defaults target_dir to "" when omitted', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        blockers: [],
        ready: [{ code: 'checkout_clone', detail: '目标目录不存在，转移执行时将 git clone 到该路径' }],
        target_dir: '~/workspace/super-debug',
        branch: 'main',
      }),
    } as Response)

    const res = await api.transferPreflight('proj-1', 'host-2')

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/proj-1/transfer/preflight'),
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ host_id: 'host-2', target_dir: '' }),
      }),
    )
    expect(res.target_dir).toBe('~/workspace/super-debug')
    expect(res.branch).toBe('main')
    expect(res.ready[0].code).toBe('checkout_clone')
  })

  it('forwards an explicit target_dir', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ blockers: [], ready: [], target_dir: '/srv/app', branch: 'main' }),
    } as Response)

    await api.transferPreflight('proj-1', 'host-2', '/srv/app')

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/proj-1/transfer/preflight'),
      expect.objectContaining({
        body: JSON.stringify({ host_id: 'host-2', target_dir: '/srv/app' }),
      }),
    )
  })

  it('surfaces blockers reported by the backend', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        blockers: [{ code: 'uncommitted', detail: '本机存在未提交的变更，请先提交或暂存后再转移' }],
        ready: [],
        target_dir: '~/workspace/super-debug',
        branch: 'main',
      }),
    } as Response)

    const res = await api.transferPreflight('proj-1', 'host-2')

    expect(res.blockers).toHaveLength(1)
    expect(res.blockers[0]).toEqual({ code: 'uncommitted', detail: '本机存在未提交的变更，请先提交或暂存后再转移' })
  })
})

describe('startTransfer', () => {
  it('POSTs host_id/target_dir and resolves the initial status snapshot', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 202,
      json: () => Promise.resolve({
        state: 'running',
        steps: [{ code: 'stop_dev', state: 'pending' }],
      }),
    } as Response)

    const res = await api.startTransfer('proj-1', 'host-2', '~/workspace/app')

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/proj-1/transfer'),
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ host_id: 'host-2', target_dir: '~/workspace/app' }),
      }),
    )
    expect(res.state).toBe('running')
    expect(res.steps[0]).toEqual({ code: 'stop_dev', state: 'pending' })
  })

  it('surfaces 409 when a transfer is already in progress', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: () => Promise.resolve({ error: '该项目已有进行中的转移' }),
    } as Response)

    await expect(api.startTransfer('proj-1', 'host-2')).rejects.toMatchObject({ status: 409 })
  })
})

describe('transferStatus', () => {
  it('GETs the status endpoint and returns steps/asset_report', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        state: 'succeeded',
        steps: [{ code: 'stop_dev', state: 'done' }, { code: 'checkout', state: 'done' }],
        asset_report: [{ code: 'missing_env_file', detail: '.env.local 缺失' }],
      }),
    } as Response)

    const res = await api.transferStatus('proj-1')

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/proj-1/transfer/status'),
      expect.any(Object),
    )
    expect(res.state).toBe('succeeded')
    expect(res.asset_report?.[0].code).toBe('missing_env_file')
  })

  it('surfaces 404 as "no transfer in progress or history" for the caller to branch on', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      statusText: 'Not Found',
      json: () => Promise.resolve({ error: 'no transfer in progress or history' }),
    } as Response)

    await expect(api.transferStatus('proj-1')).rejects.toMatchObject({ status: 404 })
  })
})

describe('transferBack', () => {
  it('POSTs to the transfer-back endpoint with no body', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 202,
      json: () => Promise.resolve({ state: 'running', steps: [] }),
    } as Response)

    const res = await api.transferBack('proj-1')

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/proj-1/transfer-back'),
      expect.objectContaining({ method: 'POST' }),
    )
    const [, callInit] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(callInit).not.toHaveProperty('body')
    expect(res.state).toBe('running')
  })
})

describe('deleteHost error surfacing', () => {
  it('exposes the stable project_home code and offending project names on 409', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: 'Conflict',
      json: () => Promise.resolve({
        code: 'project_home',
        error: '该主机仍是以下项目的归属，请先在项目概览把归属迁回本机或其他主机后再删除',
        data: { host_id: 'host-2', projects: ['super-debug', 'nova'] },
      }),
    } as Response)

    let caught: unknown
    try {
      await api.deleteHost('host-2')
    } catch (error) {
      caught = error
    }

    expect(caught).toMatchObject({
      status: 409,
      code: 'project_home',
      data: { host_id: 'host-2', projects: ['super-debug', 'nova'] },
    })
  })
})

describe('updateHost response', () => {
  it('carries homed_projects when the backend reports the dev_machine_mode true→false side effect', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        id: 'host-2',
        name: 'edge',
        tags: [],
        dev_machine_mode: false,
        homed_projects: ['super-debug'],
      }),
    } as Response)

    const res = await api.updateHost('host-2', { dev_machine_mode: false })

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/hosts/host-2'),
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ dev_machine_mode: false }) }),
    )
    expect(res.homed_projects).toEqual(['super-debug'])
  })
})
