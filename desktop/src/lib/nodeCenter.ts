/**
 * 节点中心视图模型。
 *
 * 职责：
 *   - 合并远端主机配置与 NodeRegistry 实时快照
 *   - 生成节点中心组件需要的只读渲染模型
 *   - 计算 deployment 的环境标签和异常排序
 *   - 按 host 归位端口镜像行、派生开发机标记（Task 11），供节点卡镜像区渲染
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
  MirrorStatus,
  NodeStatus,
  Project,
  RuntimeInstanceStatus,
} from '@/api/agent'
import { mirrorRowsForHost, type MirrorRowView } from './portMirrorView'

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
  /** 该 host 全部端口镜像行（跨该 host 上的所有 deployment 汇总），供节点卡镜像区渲染。 */
  mirrors: MirrorRowView[]
  /**
   * 该 host 是否被当前控制面当作开发机消费（对应 Host.dev_machine_mode）。
   * snapshot-only 节点（未在 hosts 中配置）恒为 false——dev_machine_mode 是 Host 级配置，
   * 这类节点根本没有对应的 Host 记录，无从谈起。
   */
  devMachine: boolean
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
  // mirrors 默认空数组：向后兼容尚未传入端口镜像快照的既有调用方/测试用例。
  mirrors: MirrorStatus[] = [],
): NodeCenterNode[] {
  const remoteHosts = hosts.filter(isRemoteNodeHost)
  const nodesByHost = new Map(nodeSnapshots.filter(node => node.host_id).map(node => [node.host_id, node]))
  const hostIds = new Set(remoteHosts.map(host => host.id))
  const contextByDeployment = buildDeploymentContextIndex(projects)

  const configuredNodes = remoteHosts.map(host =>
    buildNodeFromHost(host, nodesByHost.get(host.id), contextByDeployment, mirrors),
  )

  const snapshotOnlyNodes = nodeSnapshots
    .filter(node => node.host_id && !hostIds.has(node.host_id))
    .filter(node => node.host_id !== 'local')
    .filter(node => nodeDeployments(node).some(instance => !instance.is_local) || node.agent.reachable)
    .map(node => buildNodeFromSnapshot(node, contextByDeployment, mirrors))

  return [...configuredNodes, ...snapshotOnlyNodes].sort(compareNodes)
}

function buildNodeFromHost(
  host: Host,
  node: NodeStatus | undefined,
  contextByDeployment: Map<string, NodeCenterDeploymentContext>,
  mirrors: MirrorStatus[],
): NodeCenterNode {
  const deployments = node ? nodeDeployments(node) : []
  // host.dev_machine_mode 是否为 true 决定「开发机」标记；mirrors 按 host_id 独立计算——
  // agent 侧端口镜像转发只依赖 Host 配置，不依赖 NodeStatus 快照是否已到达（见
  // agent/portmirror/manager.go 的 expected-forward 计算逻辑），所以即使 node 未就绪
  // （下方 !node 分支），仍然要把该 host 的镜像行带出去，不能因为快照缺失而丢失。
  const devMachine = host.dev_machine_mode === true
  const hostMirrors = mirrorRowsForHost(host.id, mirrors)
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
      mirrors: hostMirrors,
      devMachine,
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
    mirrors: hostMirrors,
    devMachine,
  }
}

function buildNodeFromSnapshot(
  node: NodeStatus,
  contextByDeployment: Map<string, NodeCenterDeploymentContext>,
  mirrors: MirrorStatus[],
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
    mirrors: mirrorRowsForHost(node.host_id, mirrors),
    // snapshot-only 节点没有对应的 Host 记录（未被配置），dev_machine_mode 是 Host 级
    // 字段，无从取值——恒为 false，与「只有已配置的 Host 才能勾选开发机模式」的产品语义一致。
    devMachine: false,
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
