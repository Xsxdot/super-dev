/**
 * 节点中心视图模型。
 *
 * 职责：
 *   - 合并远端主机配置与 NodeRegistry 实时快照
 *   - 生成节点中心组件需要的只读渲染模型
 *   - 计算 deployment 的环境标签和异常排序
 *
 * 边界：
 *   - 不读取 Pinia store
 *   - 不渲染 UI
 *   - 不打开日志 tab 或 WebSocket
 */
import type {
  AgentRuntime,
  Health,
  Host,
  NodeStatus,
  Project,
  RuntimeInstanceStatus,
} from '@/api/agent'

const ABNORMAL_HEALTHS = new Set<Health>(['failed', 'unknown', 'restarting', 'stopped'])

export interface NodeCenterDeployment {
  instance: RuntimeInstanceStatus
  envName?: string
  projectName?: string
  abnormal: boolean
}

export interface NodeCenterDeploymentContext {
  envName: string
  projectName: string
}

export interface NodeCenterNode {
  hostId: string
  name: string
  address: string
  reachable: boolean
  muted: boolean
  agent: AgentRuntime
  deployments: NodeCenterDeployment[]
  serviceCount: number
  updatedAt?: string
  error?: string
  configured: boolean
  route?: {
    selectedType?: string
    selectedIndex: number
    degraded: boolean
  }
}

const unknownAgent: AgentRuntime = {
  installed: false,
  health: 'unknown',
  reachable: false,
}

export function isAbnormalHealth(health: Health): boolean {
  return ABNORMAL_HEALTHS.has(health)
}

export function isRemoteNodeHost(host: Host): boolean {
  return host.is_self !== true
}

export function buildDeploymentEnvIndex(projects: Project[]): Map<string, string> {
  const out = new Map<string, string>()
  for (const [deploymentId, context] of buildDeploymentContextIndex(projects)) {
    out.set(deploymentId, context.envName)
  }
  return out
}

// buildDeploymentContextIndex 生成 deployment 到项目/环境展示信息的索引。
//
// 参数：
//   - projects: 当前桌面端已加载的项目配置
//
// 返回：
//   - deployment id 到项目名和环境名的映射
//
// 注意：
//   - NodeRegistry 实时快照不包含项目维度，这里用项目配置反查，避免同名服务无法区分。
export function buildDeploymentContextIndex(projects: Project[]): Map<string, NodeCenterDeploymentContext> {
  const out = new Map<string, NodeCenterDeploymentContext>()
  for (const project of projects) {
    for (const service of project.services) {
      for (const deployment of service.deployments ?? []) {
        out.set(deployment.id, {
          envName: deployment.env_name,
          projectName: project.name,
        })
      }
    }
  }
  return out
}

export function buildNodeCenterNodes(
  hosts: Host[],
  nodeSnapshots: NodeStatus[],
  projects: Project[],
): NodeCenterNode[] {
  const remoteHosts = hosts.filter(isRemoteNodeHost)
  const nodesByHost = new Map(nodeSnapshots.filter(node => node.host_id).map(node => [node.host_id, node]))
  const hostIds = new Set(remoteHosts.map(host => host.id))
  const contextByDeployment = buildDeploymentContextIndex(projects)

  const configuredNodes = remoteHosts.map(host =>
    buildNodeFromHost(host, nodesByHost.get(host.id), contextByDeployment),
  )

  const snapshotOnlyNodes = nodeSnapshots
    .filter(node => node.host_id && !hostIds.has(node.host_id))
    .filter(node => node.host_id !== 'local')
    .filter(node => nodeDeployments(node).some(instance => !instance.is_local) || node.agent.reachable)
    .map(node => buildNodeFromSnapshot(node, contextByDeployment))

  return [...configuredNodes, ...snapshotOnlyNodes].sort(compareNodes)
}

function buildNodeFromHost(
  host: Host,
  node: NodeStatus | undefined,
  contextByDeployment: Map<string, NodeCenterDeploymentContext>,
): NodeCenterNode {
  const deployments = node ? nodeDeployments(node) : []
  if (!node) {
    return {
      hostId: host.id,
      name: host.name || host.id,
      address: host.public_ip || host.private_ip || host.id,
      reachable: false,
      muted: true,
      agent: { ...unknownAgent },
      deployments: [],
      serviceCount: 0,
      configured: true,
    }
  }
  return {
    hostId: host.id,
    name: node.name || host.name || host.id,
    address: host.public_ip || host.private_ip || host.id,
    reachable: node.reachable,
    muted: !node.reachable,
    agent: node.agent,
    deployments: sortDeployments(deployments, contextByDeployment),
    serviceCount: deployments.length,
    updatedAt: node.updated_at,
    error: node.error,
    configured: true,
    route: routeFromNode(node),
  }
}

function buildNodeFromSnapshot(
  node: NodeStatus,
  contextByDeployment: Map<string, NodeCenterDeploymentContext>,
): NodeCenterNode {
  const deployments = nodeDeployments(node)
  return {
    hostId: node.host_id,
    name: node.name || node.host_id,
    address: node.host_id,
    reachable: node.reachable,
    muted: !node.reachable,
    agent: node.agent,
    deployments: sortDeployments(deployments, contextByDeployment),
    serviceCount: deployments.length,
    updatedAt: node.updated_at,
    error: node.error,
    configured: false,
    route: routeFromNode(node),
  }
}

function routeFromNode(node: NodeStatus) {
  return node.route
    ? {
        selectedType: node.route.selected_type,
        selectedIndex: node.route.selected_index,
        degraded: node.route.degraded,
      }
    : undefined
}

function nodeDeployments(node: NodeStatus): RuntimeInstanceStatus[] {
  // 旧版/不可达节点可能从 Go nil slice 序列化出 deployments:null；视图层按空数组降级，避免状态线中断。
  return Array.isArray(node.deployments) ? node.deployments : []
}

function sortDeployments(
  instances: RuntimeInstanceStatus[],
  contextByDeployment: Map<string, NodeCenterDeploymentContext>,
): NodeCenterDeployment[] {
  return instances
    .filter(instance => !instance.is_local)
    .map(instance => {
      const context = contextByDeployment.get(instance.deployment_id)
      return {
        instance,
        envName: context?.envName,
        projectName: context?.projectName,
        abnormal: isAbnormalHealth(instance.metrics.health),
      }
    })
    .sort((left, right) => {
      if (left.abnormal !== right.abnormal) return left.abnormal ? -1 : 1
      const nameDiff = left.instance.service_name.localeCompare(right.instance.service_name)
      if (nameDiff) return nameDiff
      const envDiff = (left.envName ?? '').localeCompare(right.envName ?? '')
      if (envDiff) return envDiff
      const projectDiff = (left.projectName ?? '').localeCompare(right.projectName ?? '')
      if (projectDiff) return projectDiff
      return left.instance.deployment_id.localeCompare(right.instance.deployment_id)
    })
}

function compareNodes(left: NodeCenterNode, right: NodeCenterNode): number {
  if (left.reachable !== right.reachable) return left.reachable ? -1 : 1
  const serviceCountDiff = right.serviceCount - left.serviceCount
  if (serviceCountDiff) return serviceCountDiff
  return left.name.localeCompare(right.name) || left.hostId.localeCompare(right.hostId)
}
