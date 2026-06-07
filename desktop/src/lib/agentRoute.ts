/**
 * Agent 连接链展示辅助函数。
 *
 * 职责：
 *   - 将配置态 transport.chain 和运行态 route.last_results 合并为稳定视图模型
 *   - 统一 direct/tunnel 地址展示，避免列表与面板各自拼接文案
 *
 * 边界：
 *   - 不判断生命周期阶段
 *   - 不访问 Pinia store
 *   - 不执行探测请求
 */
import type { AgentDTO, NodeStatus, ProbeResult, TransportEntry, TransportType } from '@/api/agent'

export type AgentRouteRowStatus = 'reachable' | 'failed' | 'untested'
export type AgentRouteRole = 'primary' | 'fallback'

export interface AgentRouteSummary {
  selectedIndex: number
  selectedType?: TransportType
  address: string
  count: number
  degraded: boolean
}

export interface AgentRouteRow {
  index: number
  entry: TransportEntry
  type: TransportType
  address: string
  current: boolean
  role: AgentRouteRole
  status: AgentRouteRowStatus
  probe?: ProbeResult
  latencyMs?: number
  error?: string
}

export function transportAddress(entry?: TransportEntry): string {
  if (!entry) return '-'
  if (entry.type === 'direct') return entry.direct?.address?.trim() || 'direct'
  if (entry.type === 'tunnel') return `:${entry.tunnel?.remote_agent_port || 57017}`
  return entry.type
}

export function agentRouteSummary(agent: AgentDTO, node?: NodeStatus): AgentRouteSummary {
  const chain = agent.transport.chain
  const count = chain.length
  const boundedIndex = selectedIndex(agent, node)
  const entry = chain[boundedIndex]
  return {
    selectedIndex: boundedIndex,
    selectedType: node?.route?.selected_type ?? entry?.type,
    address: transportAddress(entry),
    count,
    degraded: Boolean(node?.route?.degraded || boundedIndex > 0),
  }
}

export function agentRouteRows(agent: AgentDTO, node?: NodeStatus): AgentRouteRow[] {
  const selected = selectedIndex(agent, node)
  const results = new Map<number, ProbeResult>()
  for (const result of node?.route?.last_results ?? []) {
    results.set(result.index, result)
  }

  return agent.transport.chain.map((entry, index) => {
    const probe = results.get(index)
    const status: AgentRouteRowStatus = probe ? (probe.reachable ? 'reachable' : 'failed') : 'untested'
    return {
      index,
      entry,
      type: entry.type,
      address: transportAddress(entry),
      current: index === selected,
      role: index === 0 ? 'primary' : 'fallback',
      status,
      probe,
      latencyMs: probe?.latency_ms,
      error: probe?.error,
    }
  })
}

function selectedIndex(agent: AgentDTO, node?: NodeStatus): number {
  const max = Math.max(agent.transport.chain.length - 1, 0)
  const raw = node?.route?.selected_index ?? 0
  if (!Number.isFinite(raw)) return 0
  if (raw < 0) return 0
  if (raw > max) return max
  return raw
}
