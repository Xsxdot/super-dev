/**
 * 运行状态服务矩阵聚合工具。
 *
 * 职责：
 *   - 将运行态实例按服务和环境聚合为项目概览矩阵
 *   - 默认把开发环境移出主矩阵，避免本机开发状态污染项目态势
 *   - 计算 KPI、环境健康计数、节点健康点和服务级资源摘要
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
  health: MatrixHealth
  label: string
}

export interface NodeHealthBead {
  nodeId: string
  nodeName: string
  envName: string
  health: Health
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
  'debug-running': 1,
}

function orderedEnvironments(project: Project): Array<{ name: string; isDev: boolean; order: number }> {
  const configured = [...(project.environments ?? [])]
    .sort((a, b) => (a.order || 0) - (b.order || 0))
    .map((env, index) => ({ name: env.name, isDev: !!env.is_dev, order: env.order || index + 1 }))
  return configured.length > 0 ? configured : [{ name: 'default', isDev: false, order: 1 }]
}

function healthLabel(health: MatrixHealth, healthy: number, total: number): string {
  if (total === 0) return 'Not configured'
  if (health === 'running' || health === 'healthy') return `Running ${healthy}/${total}`
  if (health === 'debug-running') return `Debug ${healthy}/${total}`
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

function envCell(envName: string, instances: RuntimeInstanceStatus[]): EnvMatrixCell {
  const healthy = instances.filter(instance => !isAbnormalHealth(instance.metrics.health)).length
  const abnormal = instances.length - healthy
  const health = worstHealth(instances)
  return {
    envName,
    instances,
    total: instances.length,
    healthy,
    abnormal,
    health,
    label: healthLabel(health, healthy, instances.length),
  }
}

function nodeHealths(instances: RuntimeInstanceStatus[]): NodeHealthBead[] {
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
        nodeHealths: nodeHealths(primaryServiceInstances),
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
