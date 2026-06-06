/**
 * deploymentNodeStatus 统一推导 deployment 在各节点上的编排与 collector 状态。
 *
 * 职责：
 *   - 将 Deployment.host_ids、Host 列表、远端 managed status 合成为 UI 可消费的节点状态
 *   - 提供日志 source_id 与节点选择的保守匹配规则
 *
 * 边界：
 *   - 不发起 HTTP 请求
 *   - 不持有 Vue/Pinia 状态
 *   - 不渲染文案，组件负责把 issue kind 翻译为界面语言
 */
import type {
  Deployment,
  Host,
  HostManagedDeploymentStatus,
  LogEntry,
  ManagedCollectorStatus,
} from '@/api/agent'

export type DeploymentNodeHealth = 'healthy' | 'warning' | 'failed' | 'unknown'

export type DeploymentNodeIssueKind =
  | 'unchecked'
  | 'tunnel-disconnected'
  | 'host-error'
  | 'collector-missing'
  | 'collector-stopped'
  | 'collector-failed'
  | 'collector-error'

export interface DeploymentNodeIssue {
  kind: DeploymentNodeIssueKind
  detail?: string
}

export interface DeploymentNodeState {
  hostId: string
  hostName: string
  sourceIds: string[]
  health: DeploymentNodeHealth
  collectorExpected: boolean
  collectorReady: boolean
  tunnelConnected: boolean | null
  issue?: DeploymentNodeIssue
  collector?: ManagedCollectorStatus
}

export interface DeploymentAggregateNodeStatus {
  total: number
  ready: number
  failed: number
  unknown: number
  collectorReady: number
  collectorExpected: number
  health: DeploymentNodeHealth
  nodes: DeploymentNodeState[]
}

function unique(values: Array<string | undefined | null>): string[] {
  return [...new Set(values.filter((value): value is string => !!value && value.trim().length > 0))]
}

function deploymentNeedsCollector(deployment: Deployment): boolean {
  return !!deployment.logs || !!deployment.log_type || !!deployment.log_target
}

function findCollector(
  status: HostManagedDeploymentStatus | undefined,
  deploymentId: string,
): ManagedCollectorStatus | undefined {
  return status?.remote?.collectors?.find(item => item.deployment_id === deploymentId)
}

function collectorIssue(collector: ManagedCollectorStatus): DeploymentNodeIssue | undefined {
  if (collector.error) return { kind: 'collector-error', detail: collector.error }
  if (!collector.running) return { kind: 'collector-stopped' }
  if (collector.status === 'failed') return { kind: 'collector-failed' }
  return undefined
}

function aggregateHealth(nodes: DeploymentNodeState[]): DeploymentNodeHealth {
  if (nodes.length === 0) return 'unknown'
  const ready = nodes.filter(node => node.health === 'healthy').length
  const failed = nodes.filter(node => node.health === 'failed').length
  const unknown = nodes.filter(node => node.health === 'unknown').length
  if (ready === nodes.length) return 'healthy'
  if (unknown === nodes.length) return 'unknown'
  if (failed > 0 && ready === 0 && unknown === 0) return 'failed'
  return 'warning'
}

function buildLocalNode(deployment: Deployment): DeploymentNodeState {
  const failed = deployment.status === 'failed'
  const running = deployment.status === 'running' || deployment.status === 'starting'
  return {
    hostId: 'local',
    hostName: 'local',
    sourceIds: ['local'],
    health: failed ? 'failed' : running ? 'healthy' : 'unknown',
    collectorExpected: false,
    collectorReady: running,
    tunnelConnected: true,
  }
}

/**
 * buildDeploymentNodeStatus 返回 deployment 的节点级状态聚合。
 *
 * 参数：
 *   - deployment: 目标 deployment
 *   - hosts: 当前已知 Host 列表，用于补齐名称和 node_id
 *   - managedStatuses: 按 host_id 缓存的远端编排状态
 *
 * 返回：
 *   - 节点列表、节点健康聚合、collector 就绪计数
 *
 * 注意：
 *   - 远端日志采集依赖 collector；当 deployment 配了日志但远端 status 未出现 collector 时视为异常
 */
export function buildDeploymentNodeStatus(
  deployment: Deployment,
  hosts: Host[],
  managedStatuses: Map<string, HostManagedDeploymentStatus>,
): DeploymentAggregateNodeStatus {
  const nodes = deployment.location === 'remote'
    ? buildRemoteNodes(deployment, hosts, managedStatuses)
    : [buildLocalNode(deployment)]
  const ready = nodes.filter(node => node.health === 'healthy').length
  const failed = nodes.filter(node => node.health === 'failed').length
  const unknown = nodes.filter(node => node.health === 'unknown').length
  return {
    total: nodes.length,
    ready,
    failed,
    unknown,
    collectorReady: nodes.filter(node => node.collectorReady).length,
    collectorExpected: nodes.filter(node => node.collectorExpected).length,
    health: aggregateHealth(nodes),
    nodes,
  }
}

function buildRemoteNodes(
  deployment: Deployment,
  hosts: Host[],
  managedStatuses: Map<string, HostManagedDeploymentStatus>,
): DeploymentNodeState[] {
  const expectedCollector = deploymentNeedsCollector(deployment)
  const hostMap = new Map(hosts.map(host => [host.id, host]))
  return unique(deployment.host_ids ?? []).map((hostId) => {
    const host = hostMap.get(hostId)
    const status = managedStatuses.get(hostId)
    const collector = findCollector(status, deployment.id)
    const sourceIds = unique([hostId, host?.node_id])
    const base: Omit<DeploymentNodeState, 'health' | 'collectorReady'> = {
      hostId,
      hostName: host?.name || status?.host_name || hostId,
      sourceIds,
      collectorExpected: expectedCollector || !!collector,
      tunnelConnected: status ? status.tunnel_connected : null,
      collector,
    }

    if (!status) {
      return { ...base, health: 'unknown', collectorReady: false, issue: { kind: 'unchecked' } }
    }
    if (status.error) {
      return { ...base, health: 'failed', collectorReady: false, issue: { kind: 'host-error', detail: status.error } }
    }
    if (!status.tunnel_connected) {
      return { ...base, health: 'failed', collectorReady: false, issue: { kind: 'tunnel-disconnected' } }
    }

    if (collector) {
      const issue = collectorIssue(collector)
      return {
        ...base,
        health: issue ? 'failed' : 'healthy',
        collectorReady: !issue,
        issue,
      }
    }

    if (expectedCollector) {
      return { ...base, health: 'failed', collectorReady: false, issue: { kind: 'collector-missing' } }
    }

    return { ...base, health: 'healthy', collectorReady: true }
  })
}

/**
 * logMatchesSelectedNodes 判断日志是否应展示在当前节点筛选下。
 *
 * 参数：
 *   - log: 日志条目
 *   - allNodes: deployment 的全部节点
 *   - selectedHostIds: 用户当前勾选的 host_id
 *
 * 返回：
 *   - true 表示日志应保留展示
 *
 * 注意：
 *   - 没有 source_id 或 source_id 还无法映射到已知节点时保留日志，避免后端来源字段缺失导致前端误藏日志
 */
export function logMatchesSelectedNodes(
  log: Pick<LogEntry, 'source_id'>,
  allNodes: DeploymentNodeState[],
  selectedHostIds: string[],
): boolean {
  if (allNodes.length === 0 || selectedHostIds.length === allNodes.length) return true
  if (!log.source_id) return true

  const knownSources = new Map<string, string>()
  for (const node of allNodes) {
    for (const sourceId of node.sourceIds) knownSources.set(sourceId, node.hostId)
  }
  const hostId = knownSources.get(log.source_id)
  if (!hostId) return true
  return selectedHostIds.includes(hostId)
}
