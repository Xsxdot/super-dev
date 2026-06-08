// runtimePivot 测试覆盖运行态实例两级维度透视分组。
//
// 职责：
//   - 验证不同一级/二级维度组合下的稳定分组结果
//   - 验证空输入与维度重复等边界情况
//
// 边界：
//   - 不测试组件渲染与 settingsStore 持久化
import { describe, it, expect } from 'vitest'
import { pivotInstances } from '../runtimePivot'
import type { RuntimeInstanceStatus } from '@/api/agent'

function inst(over: Partial<RuntimeInstanceStatus> & {
  service_name: string; env_name: string; node_name: string
}): RuntimeInstanceStatus {
  return {
    service_id: over.service_id ?? over.service_name,
    service_name: over.service_name,
    env_name: over.env_name,
    deployment_id: over.deployment_id ?? `${over.service_name}-${over.env_name}`,
    node_id: over.node_id ?? over.node_name,
    node_name: over.node_name,
    is_local: over.is_local ?? false,
    error: over.error,
    metrics: over.metrics ?? {
      cpu_percent: 1, mem_bytes: 1024, uptime_sec: 60, restarts: 0,
      health: 'running', base: 'systemd',
    },
  }
}

const sample: RuntimeInstanceStatus[] = [
  inst({ service_name: 'server', env_name: 'prod', node_name: 'local-01' }),
  inst({ service_name: 'audio', env_name: 'prod', node_name: 'local-01' }),
  inst({
    service_name: 'server',
    env_name: 'dev',
    node_name: '本机',
    metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: null, health: 'stopped', base: 'command' },
  }),
]

describe('pivotInstances', () => {
  it('env -> service: 一级=环境, 二级=服务, 叶子带实例', () => {
    const groups = pivotInstances(sample, 'env', 'service')
    expect(groups.map(g => g.key)).toEqual(['prod', 'dev'])
    const prod = groups[0]
    expect(prod.children.map(c => c.key)).toEqual(['server', 'audio'])
    expect(prod.children[0].instances).toHaveLength(1)
    expect(prod.children[0].instances[0].node_name).toBe('local-01')
  })

  it('service -> env: 一级=服务, 二级=环境', () => {
    const groups = pivotInstances(sample, 'service', 'env')
    expect(groups.map(g => g.key)).toEqual(['server', 'audio'])
    const server = groups.find(g => g.key === 'server')!
    expect(server.children.map(c => c.key)).toEqual(['prod', 'dev'])
  })

  it('node -> env: 一级=节点, 二级=环境', () => {
    const groups = pivotInstances(sample, 'node', 'env')
    expect(groups.map(g => g.key).sort()).toEqual(['local-01', '本机'])
  })

  it('空输入返回空数组', () => {
    expect(pivotInstances([], 'env', 'service')).toEqual([])
  })

  it('primary 与 secondary 相同时, 二级每组只含自身(退化但不崩)', () => {
    const groups = pivotInstances(sample, 'env', 'env')
    expect(groups.every(g => g.children.length === 1)).toBe(true)
  })
})
