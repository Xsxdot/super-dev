/**
 * 运行状态服务矩阵聚合工具。
 *
 * 职责：
 *   - 将运行态实例按服务和环境聚合为项目概览矩阵
 *   - 默认把开发环境移出主矩阵，避免本机开发状态污染项目态势
 *   - 计算 KPI、环境健康计数、节点健康点和服务级资源摘要
 *   - 给每个节点健康点显式标注是否属于 dev 环境（NodeHealthBead.isDev），供
 *     ServiceMatrixTable 判断要不要叠加项目归属标注（Task 12）
 *
 * 边界：
 *   - 不读取 Pinia store
 *   - 不拉取或刷新运行态
 *   - 不执行服务启停或日志打开动作
 */
import type { Health, Project, RuntimeInstanceStatus } from '@/api/agent'
import { isAbnormalHealth } from '@/lib/runtimeMetrics'

export type MatrixHealth = Health | 'not_configured'

export interface EnvMatrixCell {
  envName: string
  instances: RuntimeInstanceStatus[]
  total: number
  healthy: number
  abnormal: number
  debuggingCount: number
  health: MatrixHealth
  label: string
}

export interface NodeHealthBead {
  nodeId: string
  nodeName: string
  envName: string
  health: Health
  /**
   * isDev 标记该节点所属环境是否为 dev 环境（project.environments 里的 is_dev）。
   *
   * 注意：
   *   - 必须在 buildServiceMatrix() 构造时按原始 env.isDev 显式写入，不能靠
   *     调用方拿 envName 去 matrix.devEnvironments.includes(envName) 反推——
   *     devEnvironments 是「被隔离出主矩阵的 dev 环境名单」，只在项目同时存在
   *     非 dev 环境时才非空；只有 dev 环境的项目会回退把 dev 环境本身当作主
   *     矩阵列，这种回退分支下 devEnvironments 恒为空数组，即便这里的节点
   *     确确实实来自 dev 环境。用 devEnvironments 反推会让「dev-only 项目」
   *     这个 nodeHealths 里唯一会出现 dev 节点的场景永远判不出来（Task 12
   *     矩阵归属标注曾经踩过这个坑：标注对任何真实数据都不生效）。
   */
  isDev: boolean
}

export interface ServiceMatrixRow {
  serviceId: string
  serviceName: string
  envs: EnvMatrixCell[]
  devEnvs: EnvMatrixCell[]
  instances: RuntimeInstanceStatus[]
  nodeHealths: NodeHealthBead[]
  abnormal: number
  total: number
  cpuPercent: number | null
  memBytes: number | null
}

export interface ServiceMatrixKpis {
  critical: number
  services: number
  instances: number
  envs: Array<{ envName: string; healthy: number; total: number }>
}

export interface LocalDevSummary {
  healthy: number
  total: number
  instances: number
}

export interface ServiceMatrix {
  environments: string[]
  devEnvironments: string[]
  rows: ServiceMatrixRow[]
  kpis: ServiceMatrixKpis
  localDev: LocalDevSummary
  preferredServiceId: string
}

const HEALTH_PRIORITY: Record<Health, number> = {
  failed: 5,
  stopped: 4,
  restarting: 3,
  unknown: 2,
  running: 1,
  healthy: 1,
}

function orderedEnvironments(project: Project): Array<{ name: string; isDev: boolean; order: number }> {
  const configured = [...(project.environments ?? [])]
    .sort((a, b) => (a.order || 0) - (b.order || 0))
    .map((env, index) => ({ name: env.name, isDev: !!env.is_dev, order: env.order || index + 1 }))
  return configured.length > 0 ? configured : [{ name: 'default', isDev: false, order: 1 }]
}

function healthLabel(health: MatrixHealth, healthy: number, total: number, debuggingCount: number): string {
  if (total === 0) return 'Not configured'
  if (debuggingCount > 0 && (health === 'running' || health === 'healthy')) return `Debug ${debuggingCount}/${total}`
  if (health === 'running' || health === 'healthy') return `Running ${healthy}/${total}`
  const label = health.charAt(0).toUpperCase() + health.slice(1)
  return `${label} ${healthy}/${total}`
}

function worstHealth(instances: RuntimeInstanceStatus[]): MatrixHealth {
  if (instances.length === 0) return 'not_configured'
  return [...instances]
    .sort((a, b) => HEALTH_PRIORITY[b.metrics.health] - HEALTH_PRIORITY[a.metrics.health])[0]
    .metrics.health
}

function average(values: number[]): number | null {
  if (values.length === 0) return null
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function sum(values: number[]): number | null {
  if (values.length === 0) return null
  return values.reduce((total, value) => total + value, 0)
}

function hasActiveDebugger(instance: RuntimeInstanceStatus): boolean {
  const state = instance.debugger?.state
  return state === 'attached' || state === 'paused'
}

function envCell(envName: string, instances: RuntimeInstanceStatus[]): EnvMatrixCell {
  const healthy = instances.filter(instance => !isAbnormalHealth(instance.metrics.health)).length
  const abnormal = instances.length - healthy
  const debuggingCount = instances.filter(hasActiveDebugger).length
  const health = worstHealth(instances)
  return {
    envName,
    instances,
    total: instances.length,
    healthy,
    abnormal,
    debuggingCount,
    health,
    label: healthLabel(health, healthy, instances.length, debuggingCount),
  }
}

// nodeHealths 把实例列表投影为节点点位。envIsDev 是 envName -> is_dev 的原始
// 查找表（在 orderedEnvironments() 解析出的环境元数据上直接建立），isDev 必须
// 从这里写入，而不是事后用 devEnvironments 数组反推——理由见 NodeHealthBead.isDev
// 的类型注释。
function nodeHealths(instances: RuntimeInstanceStatus[], envIsDev: Map<string, boolean>): NodeHealthBead[] {
  return [...instances]
    .sort((a, b) => {
      const byHealth = HEALTH_PRIORITY[b.metrics.health] - HEALTH_PRIORITY[a.metrics.health]
      if (byHealth !== 0) return byHealth
      return a.node_name.localeCompare(b.node_name)
    })
    .map(instance => ({
      nodeId: instance.node_id,
      nodeName: instance.node_name,
      envName: instance.env_name,
      health: instance.metrics.health,
      isDev: envIsDev.get(instance.env_name) ?? false,
    }))
}

function valuesFor(
  instances: RuntimeInstanceStatus[],
  pick: (instance: RuntimeInstanceStatus) => number | null,
): number[] {
  return instances.map(pick).filter((value): value is number => value != null)
}

// buildServiceMatrix 将项目运行态扁平实例聚合为服务矩阵。
//
// 参数：
//   - project: 当前项目配置，用于稳定服务与环境顺序
//   - instances: RuntimeStatusTab 投影出的扁平实例
//
// 返回：生产优先的服务矩阵、项目 KPI、Local Dev 摘要和默认聚焦服务 ID。
//
// 注意：
//   - 存在非 dev 环境时，dev 只出现在详情辅助区，不进入主矩阵和 Critical
//   - 只有 dev 环境的项目回退用 dev 作为主矩阵，避免空白
export function buildServiceMatrix(project: Project, instances: RuntimeInstanceStatus[]): ServiceMatrix {
  const envs = orderedEnvironments(project)
  // envIsDev 保留每个环境名到 is_dev 的原始映射，供 nodeHealths() 直接写入
  // NodeHealthBead.isDev——devEnvironments（下面几行）是派生出来的「主矩阵之外
  // 的 dev 环境名单」，语义不同，不能拿来反推单个节点是否属于 dev 环境。
  const envIsDev = new Map(envs.map(env => [env.name, env.isDev]))
  const nonDevEnvs = envs.filter(env => !env.isDev).map(env => env.name)
  const primaryEnvironments = nonDevEnvs.length > 0 ? nonDevEnvs : envs.map(env => env.name)
  const devEnvironments = nonDevEnvs.length > 0 ? envs.filter(env => env.isDev).map(env => env.name) : []
  const primaryEnvSet = new Set(primaryEnvironments)
  const devEnvSet = new Set(devEnvironments)
  const primaryInstances = instances.filter(instance => primaryEnvSet.has(instance.env_name))
  const localDevInstances = instances.filter(instance => devEnvSet.has(instance.env_name))

  const rows = [...project.services]
    .sort((a, b) => a.order - b.order)
    .map(service => {
      const serviceInstances = instances.filter(instance => instance.service_id === service.id)
      const primaryServiceInstances = serviceInstances.filter(instance => primaryEnvSet.has(instance.env_name))
      const envCells = primaryEnvironments.map(envName =>
        envCell(envName, primaryServiceInstances.filter(instance => instance.env_name === envName)),
      )
      const devCells = devEnvironments.map(envName =>
        envCell(envName, serviceInstances.filter(instance => instance.env_name === envName)),
      )
      const abnormal = primaryServiceInstances.filter(instance => isAbnormalHealth(instance.metrics.health)).length

      return {
        serviceId: service.id,
        serviceName: service.name,
        envs: envCells,
        devEnvs: devCells,
        instances: serviceInstances,
        nodeHealths: nodeHealths(primaryServiceInstances, envIsDev),
        abnormal,
        total: primaryServiceInstances.length,
        cpuPercent: average(valuesFor(primaryServiceInstances, instance => instance.metrics.cpu_percent)),
        memBytes: sum(valuesFor(primaryServiceInstances, instance => instance.metrics.mem_bytes)),
      }
    })

  const kpis = {
    critical: primaryInstances.filter(instance => isAbnormalHealth(instance.metrics.health)).length,
    services: rows.length,
    instances: primaryInstances.length,
    envs: primaryEnvironments.map(envName => {
      const envInstances = primaryInstances.filter(instance => instance.env_name === envName)
      return {
        envName,
        healthy: envInstances.filter(instance => !isAbnormalHealth(instance.metrics.health)).length,
        total: envInstances.length,
      }
    }),
  }

  const localDev = {
    healthy: localDevInstances.filter(instance => !isAbnormalHealth(instance.metrics.health)).length,
    total: localDevInstances.length,
    instances: localDevInstances.length,
  }

  return {
    environments: primaryEnvironments,
    devEnvironments,
    rows,
    kpis,
    localDev,
    preferredServiceId: rows.find(row => row.abnormal > 0)?.serviceId ?? rows[0]?.serviceId ?? '',
  }
}
