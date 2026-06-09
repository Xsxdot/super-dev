import type { LogEntry } from '@/api/agent'

const SYNTHETIC_LOG_ID_START = 1_000_000_000_000
let nextSyntheticLogId = SYNTHETIC_LOG_ID_START

/** Client-side fields added during ingest (not from agent API). */
export interface DisplayLogEntry extends LogEntry {
  repeat_count: number
}

/** 实时事件：新行或对现有折叠行的计数增量（二选一）。 */
export interface LogEventNew {
  entry: DisplayLogEntry
}

export interface LogEventIncrement {
  increment: { deployment_id: string; fold_key: string; repeat_count: number }
}

export type LogEvent = LogEventNew | LogEventIncrement

function displayLogId(log: LogEntry): string {
  const rawID = String(log.id ?? '')
  if (rawID !== '' && rawID !== '0') {
    return log.source_id ? `${log.source_id}:${rawID}` : rawID
  }
  // 实时 WebSocket 日志尚未写入 SQLite，后端 id 为 0；前端补稳定 id 供渲染和书签去重。
  return `synthetic-${nextSyntheticLogId++}`
}

/**
 * toDisplayEntry 将 API 日志转换为前端可渲染日志。
 *
 * 注意：折叠签名计算已下沉到 agent（唯一权威），前端不再计算 normalize。
 */
export function toDisplayEntry(log: LogEntry): DisplayLogEntry {
  return {
    ...log,
    id: displayLogId(log),
    repeat_count: log.repeat_count ?? 1,
  }
}

/**
 * applyEvent 把一个实时事件应用到展示列表。
 *
 * - entry 事件：追加新行（后端已折叠，直接渲染其 repeat_count）。
 * - increment 事件：按 fold_key 找到现有行，就地更新计数；找不到则忽略
 *   （该段首行可能尚未到达或已被裁剪）。
 *
 * 前端不计算折叠签名，只认后端给的 fold_key。
 */
export function applyEvent(entries: DisplayLogEntry[], ev: LogEvent): void {
  if ('entry' in ev) {
    entries.push(ev.entry)
    return
  }
  const { deployment_id, fold_key, repeat_count } = ev.increment
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i].deployment_id === deployment_id && entries[i].fold_key === fold_key) {
      entries[i] = { ...entries[i], repeat_count }
      return
    }
  }
}
