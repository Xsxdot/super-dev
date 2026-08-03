/**
 * nodeCenter view-model tests.
 *
 * Responsibilities:
 *   - Verify remote hosts remain visible even when nodeStore has no snapshot
 *   - Verify local hosts are excluded from the global Node Center
 *   - Verify env labels and abnormal service ordering are computed outside Vue components
 *   - Verify port-mirror rows land on the right host and the devMachine flag is derived
 *     from Host.dev_machine_mode (Task 11)
 *
 * Boundaries:
 *   - Does not render Vue components
 *   - Does not open workspace tabs or WebSocket connections
 */
import { describe, expect, it } from 'vitest'
import type { Host, MirrorStatus, NodeStatus, Project, RuntimeInstanceStatus } from '@/api/agent'
import { buildDeploymentContextIndex, buildDeploymentEnvIndex, buildNodeCenterNodes } from '../nodeCenter'

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
    env_name: 'prod',
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

function mirrorStatus(partial: Partial<MirrorStatus> = {}): MirrorStatus {
  return {
    host_id: 'host-1',
    host_name: 'ali-01',
    deployment_id: 'dep-api',
    service_name: 'api',
    port: 9100,
    state: 'active',
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

  it('includes snapshot-only remote nodes in the connectivity and service-count order', () => {
    const nodes = buildNodeCenterNodes(
      [host({ id: 'host-1', name: 'ali-01' })],
      [node({ host_id: 'host-2', name: 'tokyo-01', deployments: [instance({ node_id: 'host-2', node_name: 'tokyo-01' })] })],
      [],
    )

    expect(nodes.map(item => item.hostId)).toEqual(['host-2', 'host-1'])
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

  it('adds project labels by deployment id so same-name services can be distinguished', () => {
    const nodes = buildNodeCenterNodes(
      [host()],
      [node({
        deployments: [
          instance({ deployment_id: 'dep-api-a', service_id: 'svc-api-a', service_name: 'server' }),
          instance({ deployment_id: 'dep-api-b', service_id: 'svc-api-b', service_name: 'server' }),
        ],
      })],
      [
        {
          id: 'proj-1',
          name: 'Billing API',
          root_path: '/tmp/billing',
          services: [{
            id: 'svc-api-a',
            project_id: 'proj-1',
            name: 'server',
            status: 'running',
            required: false,
            order: 1,
            deployments: [{ id: 'dep-api-a', env_name: 'prod', location: 'remote', status: 'running' }],
          }],
          environments: [{ id: 'env-prod', name: 'prod', is_dev: false, order: 1 }],
        },
        {
          id: 'proj-2',
          name: 'Admin Console',
          root_path: '/tmp/admin',
          services: [{
            id: 'svc-api-b',
            project_id: 'proj-2',
            name: 'server',
            status: 'running',
            required: false,
            order: 1,
            deployments: [{ id: 'dep-api-b', env_name: 'prod', location: 'remote', status: 'running' }],
          }],
          environments: [{ id: 'env-prod', name: 'prod', is_dev: false, order: 1 }],
        },
      ],
    )

    const projectLabels = Object.fromEntries(
      nodes[0].deployments.map(item => [item.instance.deployment_id, item.projectName]),
    )
    expect(projectLabels).toEqual({
      'dep-api-a': 'Billing API',
      'dep-api-b': 'Admin Console',
    })
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

  it('builds deployment context from all projects', () => {
    const contextIndex = buildDeploymentContextIndex([project()])

    expect(contextIndex.get('dep-api')).toEqual({ envName: 'prod', projectName: 'Demo' })
    expect(contextIndex.get('missing')).toBeUndefined()
  })

  it('keeps current route status from node snapshots', () => {
    const nodes = buildNodeCenterNodes(
      [host()],
      [node({ route: { selected_index: 1, selected_type: 'tunnel', degraded: true } })],
      [],
    )

    expect(nodes[0].route).toEqual({ selectedIndex: 1, selectedType: 'tunnel', degraded: true })
  })

  it('sorts reachable nodes before disconnected nodes and then by remote service count', () => {
    const nodes = buildNodeCenterNodes(
      [
        host({ id: 'small', name: 'small' }),
        host({ id: 'offline', name: 'offline' }),
        host({ id: 'large', name: 'large' }),
        host({ id: 'empty', name: 'empty' }),
      ],
      [
        node({
          host_id: 'small',
          name: 'small',
          deployments: [instance({ deployment_id: 'dep-small', node_id: 'small' })],
        }),
        node({
          host_id: 'offline',
          name: 'offline',
          reachable: false,
          agent: { installed: true, health: 'unreachable', reachable: false },
          deployments: [
            instance({ deployment_id: 'dep-offline-a', node_id: 'offline' }),
            instance({ deployment_id: 'dep-offline-b', node_id: 'offline' }),
          ],
        }),
        node({
          host_id: 'large',
          name: 'large',
          deployments: [
            instance({ deployment_id: 'dep-large-a', node_id: 'large' }),
            instance({ deployment_id: 'dep-large-b', node_id: 'large' }),
            instance({ deployment_id: 'dep-large-c', node_id: 'large' }),
          ],
        }),
      ],
      [],
    )

    expect(nodes.map(item => item.hostId)).toEqual(['large', 'small', 'offline', 'empty'])
  })

  describe('端口镜像归位与开发机标记（Task 11）', () => {
    it('mirrors 按 host_id 归位到对应节点卡，不混入其他主机的行', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'host-1' }), host({ id: 'host-2', name: 'tokyo-01' })],
        [],
        [],
        [
          mirrorStatus({ host_id: 'host-1', port: 9100 }),
          mirrorStatus({ host_id: 'host-1', port: 9101 }),
          mirrorStatus({ host_id: 'host-2', port: 9200 }),
        ],
      )

      const host1 = nodes.find(item => item.hostId === 'host-1')!
      const host2 = nodes.find(item => item.hostId === 'host-2')!
      expect(host1.mirrors.map(row => row.port)).toEqual([9100, 9101])
      expect(host2.mirrors.map(row => row.port)).toEqual([9200])
    })

    it('devMachine 标记直接取自 Host.dev_machine_mode', () => {
      const nodes = buildNodeCenterNodes(
        [
          host({ id: 'dev-box', name: 'dev-box', dev_machine_mode: true }),
          host({ id: 'plain', name: 'plain', dev_machine_mode: false }),
          host({ id: 'unset', name: 'unset' }),
        ],
        [],
        [],
        [],
      )

      expect(nodes.find(item => item.hostId === 'dev-box')!.devMachine).toBe(true)
      expect(nodes.find(item => item.hostId === 'plain')!.devMachine).toBe(false)
      expect(nodes.find(item => item.hostId === 'unset')!.devMachine).toBe(false)
    })

    it('尚无 node 快照（muted 占位态）时仍然携带该 host 的 mirrors——转发由本机 agent 独立计算，不依赖远端快照到达', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'host-1', dev_machine_mode: true })],
        [],
        [],
        [mirrorStatus({ host_id: 'host-1', port: 9100, state: 'pending' })],
      )

      expect(nodes[0].muted).toBe(true)
      expect(nodes[0].mirrors.map(row => row.port)).toEqual([9100])
      expect(nodes[0].devMachine).toBe(true)
    })

    it('snapshot-only 节点（未在 hosts 中配置）devMachine 恒为 false，且仍按 host_id 收到 mirrors', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'host-1' })],
        [node({
          host_id: 'host-2',
          name: 'tokyo-01',
          deployments: [instance({ node_id: 'host-2', node_name: 'tokyo-01' })],
        })],
        [],
        [mirrorStatus({ host_id: 'host-2', port: 9300 })],
      )

      const snapshotOnly = nodes.find(item => item.hostId === 'host-2')!
      expect(snapshotOnly.configured).toBe(false)
      expect(snapshotOnly.devMachine).toBe(false)
      expect(snapshotOnly.mirrors.map(row => row.port)).toEqual([9300])
    })

    it('本机 (is_self) 节点被排除在节点中心之外，不会产出携带 mirrors 的节点卡（节点中心 remote-only 的既有裁定）', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'self', name: 'local', is_self: true, dev_machine_mode: false })],
        [],
        [],
        [mirrorStatus({ host_id: 'self', port: 9100 })],
      )

      expect(nodes).toHaveLength(0)
    })

    it('省略 mirrors 参数时按空数组处理，向后兼容既有调用方（默认参数）', () => {
      const nodes = buildNodeCenterNodes([host({ dev_machine_mode: true })], [], [])

      expect(nodes[0].mirrors).toEqual([])
      expect(nodes[0].devMachine).toBe(true)
    })
  })

  describe('desktopOnline 徽标数据链（Task 10）', () => {
    it('已配置 host 透传 node.desktop_online', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'host-1' })],
        [node({ host_id: 'host-1', desktop_online: true })],
        [],
      )

      expect(nodes[0].desktopOnline).toBe(true)
    })

    it('node.desktop_online 为 false 时透传 false', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'host-1' })],
        [node({ host_id: 'host-1', desktop_online: false })],
        [],
      )

      expect(nodes[0].desktopOnline).toBe(false)
    })

    it('尚无 node 快照（muted 占位态）时 desktopOnline 恒为 false', () => {
      const nodes = buildNodeCenterNodes([host({ id: 'host-1' })], [], [])

      expect(nodes[0].desktopOnline).toBe(false)
    })

    it('snapshot-only 节点同样透传 desktop_online', () => {
      const nodes = buildNodeCenterNodes(
        [host({ id: 'host-1' })],
        [node({
          host_id: 'host-2',
          name: 'tokyo-01',
          desktop_online: true,
          deployments: [instance({ node_id: 'host-2', node_name: 'tokyo-01' })],
        })],
        [],
      )

      expect(nodes.find(item => item.hostId === 'host-2')!.desktopOnline).toBe(true)
    })

    it('host_id 为 local 的本机快照不会产出节点卡，desktopOnline 无从渲染——桌面端在场徽标只服务远程节点（既有 host_id !== \'local\' 过滤，未在此改动）', () => {
      const nodes = buildNodeCenterNodes(
        [],
        [node({ host_id: 'local', desktop_online: true })],
        [],
      )

      expect(nodes).toHaveLength(0)
    })
  })
})
