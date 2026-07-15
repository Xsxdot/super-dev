/**
 * agentBulkUpdate tests batch update target classification and worker concurrency.
 *
 * Responsibilities:
 *   - Verify default selection, manual selection, and disabled reasons
 *   - Verify semantic version comparison
 *   - Verify batch workers isolate per-host failures
 *
 * Boundaries:
 *   - Does not render Vue components
 *   - Does not call real Agent APIs
 */
import { describe, expect, it, vi } from 'vitest'
import type { AgentDTO, Host } from '@/api/agent'
import { buildBulkUpdateRows, compareAgentVersions, runAgentUpdateBatch } from '../agentBulkUpdate'

function host(overrides: Partial<Host> = {}): Host {
  return {
    id: 'h1',
    name: 'ali-01',
    tags: [],
    ssh_host: '10.0.0.8',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_credential_configured: true,
    ssh_private_key_configured: true,
    ssh_host_key_fingerprint_configured: true,
    ...overrides,
  }
}

function agent(overrides: Partial<AgentDTO> = {}): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: ['prod'],
    transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }] },
    config: { listen_port: 57017 },
    runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    security: { token_configured: true, provision_state: 'provisioned', tls: { mode: 'auto' } },
    ...overrides,
  }
}

describe('agentBulkUpdate', () => {
  it('compares semver-like agent versions', () => {
    expect(compareAgentVersions('0.1.0', '0.1.1')).toBe(-1)
    expect(compareAgentVersions('v0.1.2', '0.1.1')).toBe(1)
    expect(compareAgentVersions('0.1.0', '0.1.0')).toBe(0)
    expect(compareAgentVersions('dev', '0.1.0')).toBeNull()
  })

  it('selects outdated and unknown versions by default', () => {
    const rows = buildBulkUpdateRows(
      [
        agent({ host_id: 'h1', runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' } }),
        agent({ host_id: 'h2', host_name: 'ali-02', runtime: { installed: true, health: 'healthy', reachable: true } }),
      ],
      [host({ id: 'h1' }), host({ id: 'h2' })],
      '0.2.0',
    )

    expect(rows.map(row => row.selected)).toEqual([true, true])
    expect(rows.map(row => row.reasonKey)).toEqual([
      'settings.agents.bulkUpdateReasonOutdated',
      'settings.agents.bulkUpdateReasonUnknownVersion',
    ])
  })

  it('allows unreachable agents manually but does not select them by default', () => {
    const rows = buildBulkUpdateRows(
      [agent({ runtime: { installed: true, health: 'unreachable', reachable: false, version: '0.1.0' } })],
      [host()],
      '0.2.0',
    )

    expect(rows[0]).toMatchObject({
      eligibility: 'manual-allowed',
      selected: false,
      disabled: false,
      reasonKey: 'settings.agents.bulkUpdateReasonUnreachable',
    })
  })

  it('disables uninstalled, current, and ssh-missing agents', () => {
    const rows = buildBulkUpdateRows(
      [
        agent({ host_id: 'h1', runtime: { installed: false, health: 'unknown', reachable: false } }),
        agent({ host_id: 'h2', runtime: { installed: true, health: 'healthy', reachable: true, version: '0.2.0' } }),
        agent({ host_id: 'h3', runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' } }),
      ],
      [host({ id: 'h1' }), host({ id: 'h2' }), host({ id: 'h3', ssh_credential_configured: false })],
      '0.2.0',
    )

    expect(rows.map(row => row.disabled)).toEqual([true, true, true])
    expect(rows.map(row => row.reasonKey)).toEqual([
      'settings.agents.bulkUpdateReasonNotInstalled',
      'settings.agents.bulkUpdateReasonCurrent',
      'settings.agents.bulkUpdateReasonMissingSSH',
    ])
  })

  it('runs selected updates with concurrency and keeps going after failure', async () => {
    const starts: string[] = []
    const done: string[] = []
    const update = vi.fn(async (hostId: string) => {
      starts.push(hostId)
      if (hostId === 'h2') throw new Error('upload failed')
      done.push(hostId)
    })
    const check = vi.fn(async (hostId: string) => {
      done.push(`check:${hostId}`)
    })

    const results = await runAgentUpdateBatch(['h1', 'h2', 'h3'], 2, update, check)

    expect(starts).toEqual(['h1', 'h2', 'h3'])
    expect(results).toEqual([
      { hostId: 'h1', ok: true },
      { hostId: 'h2', ok: false, error: 'upload failed' },
      { hostId: 'h3', ok: true },
    ])
    expect(check).toHaveBeenCalledWith('h1')
    expect(check).toHaveBeenCalledWith('h3')
    expect(check).not.toHaveBeenCalledWith('h2')
  })
})
