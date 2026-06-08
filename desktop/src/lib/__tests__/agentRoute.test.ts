/**
 * Agent route display helper tests.
 *
 * 职责：
 *   - 验证 transport.chain 配置态与 NodeStatus.route 运行态的组合展示
 *   - 覆盖当前走通、探活失败、未测试和未知连接类型
 *
 * 边界：
 *   - 不渲染组件
 *   - 不执行真实探测请求
 */
import { describe, expect, it } from 'vitest'
import { agentRouteRows, agentRouteSummary, transportAddress } from '@/lib/agentRoute'
import type { AgentDTO, NodeStatus, TransportEntry } from '@/api/agent'

function agent(chain: TransportEntry[]): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: [],
    transport: { chain },
    config: { listen_port: 57017 },
    runtime: { installed: true, health: 'healthy', reachable: true },
    security: { token_configured: true, provision_state: 'provisioned', tls: { mode: 'auto' } },
  }
}

function node(route: NodeStatus['route']): NodeStatus {
  return {
    host_id: 'h1',
    name: 'ali-01',
    reachable: true,
    agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    deployments: [],
    route,
    updated_at: '2026-06-07T10:00:00Z',
  }
}

describe('transportAddress', () => {
  it('formats direct, tunnel, and unknown entries', () => {
    expect(transportAddress({ type: 'direct', direct: { address: '100.64.0.8:57017' } })).toBe('100.64.0.8:57017')
    expect(transportAddress({ type: 'tunnel', tunnel: { remote_agent_port: 57018 } })).toBe(':57018')
    expect(transportAddress({ type: 'mq' })).toBe('mq')
  })
})

describe('agentRouteSummary', () => {
  it('uses the selected fallback route when realtime route is degraded', () => {
    const dto = agent([
      { type: 'direct', direct: { address: '100.64.0.8:57017' } },
      { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
    ])

    expect(agentRouteSummary(dto, node({ selected_index: 1, selected_type: 'tunnel', degraded: true }))).toMatchObject({
      selectedIndex: 1,
      selectedType: 'tunnel',
      address: ':57017',
      count: 2,
      degraded: true,
    })
  })

  it('falls back to the first configured chain entry without route data', () => {
    const dto = agent([{ type: 'direct', direct: { address: '10.0.0.8:57017' } }])

    expect(agentRouteSummary(dto, undefined)).toMatchObject({
      selectedIndex: 0,
      selectedType: 'direct',
      address: '10.0.0.8:57017',
      count: 1,
      degraded: false,
    })
  })
})

describe('agentRouteRows', () => {
  it('merges last_results with the configured chain order', () => {
    const dto = agent([
      { type: 'direct', direct: { address: '100.64.0.8:57017' } },
      { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
      { type: 'direct', direct: { address: '10.0.0.8:57017' } },
    ])

    const rows = agentRouteRows(dto, node({
      selected_index: 1,
      selected_type: 'tunnel',
      degraded: true,
      last_results: [
        {
          index: 0,
          transport_type: 'direct',
          status: 'unreachable',
          reachable: false,
          error: 'connection refused',
          checked_at: '2026-06-07T10:00:00Z',
        },
        {
          index: 1,
          transport_type: 'tunnel',
          status: 'reachable',
          reachable: true,
          latency_ms: 12,
          checked_at: '2026-06-07T10:00:01Z',
        },
      ],
    }))

    expect(rows.map(row => ({
      index: row.index,
      address: row.address,
      current: row.current,
      status: row.status,
      role: row.role,
      error: row.error,
    }))).toEqual([
      { index: 0, address: '100.64.0.8:57017', current: false, status: 'failed', role: 'primary', error: 'connection refused' },
      { index: 1, address: ':57017', current: true, status: 'reachable', role: 'fallback', error: undefined },
      { index: 2, address: '10.0.0.8:57017', current: false, status: 'untested', role: 'fallback', error: undefined },
    ])
  })

  it('treats the current route as reachable when overall agent runtime is healthy', () => {
    const dto = agent([
      { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
    ])

    const rows = agentRouteRows(dto, node({
      selected_index: 0,
      selected_type: 'tunnel',
      degraded: false,
    }))

    expect(rows.map(row => ({
      current: row.current,
      status: row.status,
    }))).toEqual([
      { current: true, status: 'reachable' },
    ])
  })
})
