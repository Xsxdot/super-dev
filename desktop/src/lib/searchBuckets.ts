/**
 * 跨服务搜索上下文时间栅格工具。
 *
 * 职责：
 *   - 将各服务日志按固定时间桶对齐
 *   - 为缺日志服务生成空白占位 cell
 *   - 提供稳定数据结构给搜索看板渲染
 *
 * 边界：
 *   - 不读取 DOM，不计算真实像素高度
 *   - 不负责 API 请求或服务隐藏状态
 */
import type { LogEntry } from '@/api/agent'

export interface SearchBucketCell {
  serviceId: string
  entries: LogEntry[]
  blank: boolean
}

export interface SearchBucketRow {
  bucketStart: number
  bucketLabel: string
  cells: Record<string, SearchBucketCell>
}

export interface BuildSearchBucketsInput {
  serviceIds: string[]
  itemsByService: Record<string, LogEntry[]>
  bucketMs?: number
}

function bucketStart(timestamp: string, bucketMs: number): number {
  const time = new Date(timestamp).getTime()
  return Math.floor(time / bucketMs) * bucketMs
}

function bucketLabel(start: number): string {
  return new Date(start).toISOString().slice(11, 19)
}

export function buildSearchBuckets(input: BuildSearchBucketsInput): SearchBucketRow[] {
  const bucketMs = input.bucketMs ?? 1000
  const starts = new Set<number>()
  const grouped: Record<string, Record<number, LogEntry[]>> = {}

  for (const serviceId of input.serviceIds) {
    grouped[serviceId] = {}
    for (const entry of input.itemsByService[serviceId] ?? []) {
      const start = bucketStart(entry.timestamp, bucketMs)
      starts.add(start)
      grouped[serviceId][start] ??= []
      grouped[serviceId][start].push(entry)
    }
  }

  return [...starts].sort((a, b) => a - b).map(start => {
    const cells: Record<string, SearchBucketCell> = {}
    for (const serviceId of input.serviceIds) {
      const entries = grouped[serviceId]?.[start] ?? []
      cells[serviceId] = { serviceId, entries, blank: entries.length === 0 }
    }
    return { bucketStart: start, bucketLabel: bucketLabel(start), cells }
  })
}
