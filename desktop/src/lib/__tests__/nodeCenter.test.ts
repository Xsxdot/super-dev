/**
 * nodeCenter view-model tests.
 *
 * Responsibilities:
 *   - Verify remote hosts remain visible even when nodeStore has no snapshot
 *   - Verify local hosts are excluded from the global Node Center
 *   - Verify env labels and abnormal service ordering are computed outside Vue components
 *
 * Boundaries:
 *   - Does not render Vue components
 *   - Does not open workspace tabs or WebSocket connections
 */
import { describe, expect, it } from 'vitest'
import type { Host, NodeStatus, Project, RuntimeInstanceStatus } from '@/api/agent'
import { buildDeploymentEnvIndex, buildNodeCenterNodes } from '../nodeCenter'

function host(partial: Partial<Host> = {}): Host {
  return {
    id: 'host-1',
    name: 'ali-01',
    private_ip: '10.0.0.8',
    tags: [],
    ...partial,
  }
}

function instance(partial: Partial<RuntimeInstanceStatus> = {}): RuntimeInstanceStatus {
  return {
    service_id: 'svc-api',
    service_name: 'api',
    deployment_id: 'dep-api',
    node_id: 'host-1',
    node_name: 'ali-01',
    is_local: false,
    metrics: {
      cpu_percent: 12.5,
      mem_bytes: 128 * 1024 * 1024,
      uptime_sec: 3661,
      restarts: 0,
      health: 'running',
      base: 'systemd',
    },
    ...partial,
  }
}

function node(partial: Partial<NodeStatus> = {}): NodeStatus {
  return {
    host_id: 'host-1',
    name: 'ali-01',
    reachable: true,
    agent: {
      installed: true,
      version: '0.1.0',
      health: 'healthy',
      reachable: true,
    },
    deployments: [instance()],
    updated_at: '2026-06-06T10:00:00Z',
    ...partial,
  }
}

function project(): Project {
  return {
    id: 'proj-1',
    name: 'Demo',
    root_path: '/tmp/demo',
    services: [{
      id: 'svc-api',
      project_id: 'proj-1',
      name: 'api',
      status: 'running',
      required: false,
      order: 1,
      deployments: [{ id: 'dep-api', env_name: 'prod', location: 'remote', status: 'running' }],
    }],
    environments: [{ id: 'env-prod', name: 'prod', is_dev: false, order: 1 }],
  }
}

describe('nodeCenter view model', () => {
  it('keeps a configured remote host as a muted placeholder when nodeStore has no snapshot', () => {
    const nodes = buildNodeCenterNodes([host()], [], [])

    expect(nodes).toHaveLength(1)
    expect(nodes[0]).toMatchObject({
      hostId: 'host-1',
      name: 'ali-01',
      reachable: false,
      muted: true,
      serviceCount: 0,
    })
    expect(nodes[0].agent.health).toBe('unknown')
  })

  it('excludes self hosts from the node center', () => {
    const nodes = buildNodeCenterNodes([
      host({ id: 'self', name: 'local', is_self: true }),
      host({ id: 'remote', name: 'remote-01' }),
    ], [], [])

    expect(nodes.map(item => item.hostId)).toEqual(['remote'])
  })

  it('includes snapshot-only remote nodes after configured hosts', () => {
    const nodes = buildNodeCenterNodes(
      [host({ id: 'host-1', name: 'ali-01' })],
      [node({ host_id: 'host-2', name: 'tokyo-01', deployments: [instance({ node_id: 'host-2', node_name: 'tokyo-01' })] })],
      [],
    )

    expect(nodes.map(item => item.hostId)).toEqual(['host-1', 'host-2'])
    expect(nodes.find(item => item.hostId === 'host-2')?.configured).toBe(false)
  })

  it('treats null deployments from unreachable node snapshots as an empty list', () => {
    const nodes = buildNodeCenterNodes(
      [host()],
      [node({
        reachable: false,
        agent: { installed: false, health: 'unreachable', reachable: false },
        deployments: null as unknown as RuntimeInstanceStatus[],
        error: 'node unreachable',
      })],
      [],
    )

    expect(nodes).toHaveLength(1)
    expect(nodes[0].deployments).toEqual([])
    expect(nodes[0].serviceCount).toBe(0)
    expect(nodes[0].error).toBe('node unreachable')
  })

  it('adds env labels by deployment id and omits missing labels', () => {
    const nodes = buildNodeCenterNodes(
      [host()],
      [node({ deployments: [instance(), instance({ deployment_id: 'dep-worker', service_name: 'worker' })] })],
      [project()],
    )

    const labels = nodes[0].deployments.map(item => item.envName)
    expect(labels).toEqual(['prod', undefined])
  })

  it('sorts abnormal deployments above healthy deployments', () => {
    const failed = instance({
      service_name: 'worker',
      deployment_id: 'dep-worker',
      metrics: {
        cpu_percent: null,
        mem_bytes: null,
        uptime_sec: null,
        restarts: 3,
        health: 'failed',
        base: 'systemd',
      },
    })
    const running = instance({ service_name: 'api', deployment_id: 'dep-api' })
    const nodes = buildNodeCenterNodes([host()], [node({ deployments: [running, failed] })], [])

    expect(nodes[0].deployments.map(item => item.instance.deployment_id)).toEqual(['dep-worker', 'dep-api'])
    expect(nodes[0].deployments[0].abnormal).toBe(true)
  })

  it('builds a deployment env index from all projects', () => {
    const envIndex = buildDeploymentEnvIndex([project()])

    expect(envIndex.get('dep-api')).toBe('prod')
    expect(envIndex.get('missing')).toBeUndefined()
  })
})
