/**
 * 运行态指标格式化工具。
 *
 * 职责：
 *   - 格式化 CPU、内存、运行时长和重启次数
 *   - 提供健康状态分类和 CPU 迷你条宽度
 *
 * 边界：
 *   - 不读取 store
 *   - 不渲染组件
 */
import type { Health } from '@/api/agent'

const ABNORMAL_HEALTHS = new Set<Health>(['failed', 'unknown', 'restarting', 'stopped'])

export function formatPercent(value: number | null, emptyText = '--'): string {
  return value == null ? emptyText : `${value.toFixed(1)}%`
}

export function formatBytes(value: number | null, emptyText = '--'): string {
  if (value == null) return emptyText
  if (value >= 1024 * 1024 * 1024) return `${(value / 1024 / 1024 / 1024).toFixed(1)} GiB`
  return `${Math.round(value / 1024 / 1024)} MiB`
}

export function formatUptime(value: number | null, emptyText = '--'): string {
  if (value == null) return emptyText
  const hours = Math.floor(value / 3600)
  const minutes = Math.floor((value % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

export function formatRestarts(value: number | null, emptyText = '--'): string {
  return value == null ? emptyText : String(value)
}

export function isAbnormalHealth(health: Health): boolean {
  return ABNORMAL_HEALTHS.has(health)
}

export function cpuBarWidth(value: number | null): number | null {
  if (value == null) return null
  return Math.min(100, Math.max(0, value))
}
