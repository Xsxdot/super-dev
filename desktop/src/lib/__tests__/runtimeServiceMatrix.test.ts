/**
 * runtimeServiceMatrix tests.
 *
 * Responsibilities:
 *   - Verify project overview runtime aggregation is production-first
 *   - Verify local dev instances stay out of primary critical counts
 *   - Verify multi-node service detail data remains available
 *
 * Boundaries:
 *   - Does not render Vue components
 *   - Does not read stores or fetch runtime status
 */
import { describe, expect, it } from 'vitest'
import { buildServiceMatrix } from '../runtimeServiceMatrix'
import type { Project, RuntimeInstanceStatus } from '@/api/agent'

function project(): Project {
  return {
    id: 'proj-1',
    name: 'TK',
    root_path: '/tmp/tk',
    environments: [
      { id: 'env-dev', name: 'dev', is_dev: true, order: 1 },
      { id: 'env-prod', name: 'prod', is_dev: false, order: 2 },
    ],
    services: [
      {
        id: 'svc-server',
        project_id: 'proj-1',
        name: 'server',
        status: '',
        required: true,
        order: 1,
        deployments: [],
      },
      {
        id: 'svc-audio',
        project_id: 'proj-1',
        name: 'audio',
        status: '',
        required: true,
        order: 2,
        deployments: [],
      },
    ],
  }
}

function devOnlyProject(): Project {
  return {
    ...project(),
    environments: [{ id: 'env-dev', name: 'dev', is_dev: true, order: 1 }],
  }
}

function inst(partial: Partial<RuntimeInstanceStatus> & {
  service_id: string
  service_name: string
  env_name: string
  node_id: string
  node_name: string
}): RuntimeInstanceStatus {
  return {
    deployment_id: `${partial.service_id}-${partial.env_name}-${partial.node_id}`,
    is_local: false,
    metrics: {
      cpu_percent: 1,
      mem_bytes: 128 * 1024 * 1024,
      uptime_sec: 3600,
      restarts: 0,
      health: 'running',
      base: 'systemd',
    },
    ...partial,
  }
}

describe('buildServiceMatrix', () => {
  it('uses non-dev environments as primary matrix columns when they exist', () => {
    const matrix = buildServiceMatrix(project(), [
      inst({
        service_id: 'svc-server',
        service_name: 'server',
        env_name: 'dev',
        node_id: 'local',
        node_name: 'MacBook-Pro.local',
        is_local: true,
        metrics: {
          cpu_percent: null,
          mem_bytes: null,
          uptime_sec: null,
          restarts: null,
          health: 'stopped',
          base: 'command',
        },
      }),
      inst({ service_id: 'svc-server', service_name: 'server', env_name: 'prod', node_id: 'n1', node_name: 'node-01' }),
    ])

    expect(matrix.environments).toEqual(['prod'])
    expect(matrix.devEnvironments).toEqual(['dev'])
    expect(matrix.kpis.critical).toBe(0)
    expect(matrix.kpis.instances).toBe(1)
    expect(matrix.localDev.instances).toBe(1)
    expect(matrix.rows[0].devEnvs[0].label).toBe('Stopped 0/1')
  })

  it('falls back to dev columns for dev-only projects', () => {
    const matrix = buildServiceMatrix(devOnlyProject(), [
      inst({
        service_id: 'svc-server',
        service_name: 'server',
        env_name: 'dev',
        node_id: 'local',
        node_name: 'MacBook-Pro.local',
      }),
    ])

    expect(matrix.environments).toEqual(['dev'])
    expect(matrix.devEnvironments).toEqual([])
    expect(matrix.kpis.instances).toBe(1)
  })

  it('aggregates multi-node production health and keeps detail instances', () => {
    const matrix = buildServiceMatrix(project(), [
      inst({ service_id: 'svc-server', service_name: 'server', env_name: 'prod', node_id: 'n1', node_name: 'node-01', metrics: { cpu_percent: 2, mem_bytes: 100 * 1024 * 1024, uptime_sec: 3600, restarts: 0, health: 'running', base: 'systemd' } }),
      inst({ service_id: 'svc-server', service_name: 'server', env_name: 'prod', node_id: 'n2', node_name: 'node-02', metrics: { cpu_percent: 4, mem_bytes: 150 * 1024 * 1024, uptime_sec: 7200, restarts: 1, health: 'running', base: 'systemd' } }),
      inst({ service_id: 'svc-server', service_name: 'server', env_name: 'prod', node_id: 'n3', node_name: 'node-03', metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: null, health: 'stopped', base: 'systemd' } }),
    ])

    const server = matrix.rows[0]
    const prod = server.envs[0]

    expect(prod.total).toBe(3)
    expect(prod.healthy).toBe(2)
    expect(prod.abnormal).toBe(1)
    expect(prod.health).toBe('stopped')
    expect(prod.label).toBe('Stopped 2/3')
    expect(server.nodeHealths.map(node => node.health)).toEqual(['stopped', 'running', 'running'])
    expect(server.cpuPercent).toBe(3)
    expect(server.memBytes).toBe(250 * 1024 * 1024)
    expect(server.instances).toHaveLength(3)
  })
})
