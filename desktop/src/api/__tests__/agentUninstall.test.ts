/**
 * Agent uninstall API contract tests.
 *
 * Responsibilities:
 *   - Verify the Desktop sends an explicit default data-retention choice
 *   - Preserve backend uninstall failure stages for recovery messaging
 *   - Verify Detach uses a separate explicit endpoint and reason
 *
 * Boundaries:
 *   - Does not call a real Agent process
 *   - Does not test Settings UI rendering
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/agent'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('agent uninstall api', () => {
  it('sends an explicit default data-retention choice', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        ok: true,
        host_id: 'h1',
        removed_data: false,
        message: 'Agent uninstalled',
      }),
    } as Response)

    await expect(api.uninstallAgent('h1', { remove_data: false })).resolves.toMatchObject({ removed_data: false })

    expect(globalThis.fetch).toHaveBeenCalledWith(expect.stringContaining('/api/agents/h1/uninstall'), expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ remove_data: false }),
    }))
  })

  it('preserves the backend failure stage for Desktop recovery messaging', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      statusText: 'Bad Gateway',
      json: () => Promise.resolve({
        error: 'connect: fixture failure',
        stage: 'remote_uninstall',
      }),
    } as Response)

    await expect(api.uninstallAgent('h1', { remove_data: false })).rejects.toMatchObject({
      status: 502,
      stage: 'remote_uninstall',
    })
  })

  it('uses a separate explicit endpoint for Controller-only detach', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ status: 'detached', host_id: 'h1' }),
    } as Response)

    await expect(api.detachAgent('h1', { reason: 'manual_uninstall_failed' })).resolves.toMatchObject({
      status: 'detached',
      host_id: 'h1',
    })

    expect(globalThis.fetch).toHaveBeenCalledWith(expect.stringContaining('/api/agents/h1/detach'), expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ reason: 'manual_uninstall_failed' }),
    }))
  })
})
