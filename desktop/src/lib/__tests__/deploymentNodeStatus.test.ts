/**
 * deploymentNodeStatus 测试远端 deployment 节点状态推导。
 *
 * 职责：
 *   - 验证 collector 状态会聚合成节点健康摘要
 *   - 验证日志 source_id 节点筛选采用保守匹配
 *
 * 边界：
 *   - 不挂载 Vue 组件
 *   - 不请求 agent API
 */
import { describe, expect, it } from 'vitest'
import {
  buildDeploymentNodeStatus,
  logMatchesSelectedNodes,
} from '@/lib/deploymentNodeStatus'
import type { Deployment, Host, HostManagedDeploymentStatus, LogEntry } from '@/api/agent'

const deployment: Deployment = {
  id: 'dep-api',
  env_name: 'prod',
  location: 'remote',
  status: 'running',
  host_ids: ['h1', 'h2', 'h3'],
  logs: { type: 'file_tail', path: '/var/log/api.log' },
}

const hosts: Host[] = [
  { id: 'h1', name: 'ali-01', private_ip: '10.0.0.1', tags: [], node_id: 'node-ali' },
  { id: 'h2', name: 'jp', private_ip: '10.0.0.2', tags: [] },
]

function statusOf(hostId: string, running: boolean): HostManagedDeploymentStatus {
  return {
    host_id: hostId,
    host_name: hostId,
    desired_deployment_count: 1,
    desired_collector_count: 1,
    tunnel_connected: true,
    remote: {
      deployment_count: 1,
      collector_count: 1,
      collectors: [{
        deployment_id: 'dep-api',
        desired: true,
        running,
        status: running ? 'running' : 'stopped',
      }],
    },
  }
}

function log(sourceId?: string): Pick<LogEntry, 'source_id'> {
  return { source_id: sourceId }
}

describe('deploymentNodeStatus', () => {
  it('按 host 聚合 collector 运行状态', () => {
    const aggregate = buildDeploymentNodeStatus(
      deployment,
      hosts,
      new Map([
        ['h1', statusOf('h1', true)],
        ['h2', statusOf('h2', false)],
      ]),
    )

    expect(aggregate.total).toBe(3)
    expect(aggregate.ready).toBe(1)
    expect(aggregate.collectorReady).toBe(1)
    expect(aggregate.collectorExpected).toBe(3)
    expect(aggregate.health).toBe('warning')
    expect(aggregate.nodes.map(node => [node.hostId, node.health])).toEqual([
      ['h1', 'healthy'],
      ['h2', 'failed'],
      ['h3', 'unknown'],
    ])
    expect(aggregate.nodes[0].sourceIds).toEqual(['h1', 'node-ali'])
  })

  it('日志筛选只隐藏可明确映射到未选节点的日志', () => {
    const aggregate = buildDeploymentNodeStatus(
      deployment,
      hosts,
      new Map([
        ['h1', statusOf('h1', true)],
        ['h2', statusOf('h2', true)],
        ['h3', statusOf('h3', true)],
      ]),
    )

    expect(logMatchesSelectedNodes(log('h1'), aggregate.nodes, ['h1'])).toBe(true)
    expect(logMatchesSelectedNodes(log('node-ali'), aggregate.nodes, ['h1'])).toBe(true)
    expect(logMatchesSelectedNodes(log('h2'), aggregate.nodes, ['h1'])).toBe(false)
    expect(logMatchesSelectedNodes(log('unknown-source'), aggregate.nodes, ['h1'])).toBe(true)
    expect(logMatchesSelectedNodes(log(), aggregate.nodes, ['h1'])).toBe(true)
  })
})
