import type { LogEntry } from '@/api/agent'

/** Client-side fields added during ingest (not from agent API). */
export interface DisplayLogEntry extends LogEntry {
  // cursor_id 保留后端原始日志游标，复制/导出给 MCP 使用；id 可被改写成 UI 展示 key。
  cursor_id?: string
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

// djb2 字符串 hash，给无 rowid 的实时日志生成稳定短 id。
function hashString(s: string): string {
  let h = 5381
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) + h + s.charCodeAt(i)) | 0
  }
  return (h >>> 0).toString(36)
}

// displayLogId 为日志生成稳定渲染 id。
//
// 有真实 rowid（落库后/历史/重连 replay）时用 source_id:id；
// 实时 WebSocket 日志 rowid 为 0，用 来源+时间戳+stream+内容 的 hash。
// 跨进程重启/重连对同一条日志幂等，供书签去重与 anchor 复位。
function displayLogId(log: LogEntry): string {
  const rawID = String(log.id ?? '')
  if (rawID !== '' && rawID !== '0') {
    return log.source_id ? `${log.source_id}:${rawID}` : rawID
  }
  const seed = `${log.source_id ?? ''}|${log.deployment_id}|${log.timestamp}|${log.stream ?? ''}|${log.message}`
  return `live-${hashString(seed)}`
}

/**
 * toDisplayEntry 将 API 日志转换为前端可渲染日志。
 *
 * 注意：折叠签名计算已下沉到 agent（唯一权威），前端不再计算 normalize。
 */
export function toDisplayEntry(log: LogEntry): DisplayLogEntry {
  const cursorId = String(log.cursor_id ?? log.id ?? '')
  return {
    ...log,
    id: displayLogId(log),
    cursor_id: cursorId,
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
export function applyEvent(entries: DisplayLogEntry[], ev: LogEvent): boolean {
  if ('entry' in ev) {
    entries.push(ev.entry)
    return true
  }
  const { deployment_id, fold_key, repeat_count } = ev.increment
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i].deployment_id === deployment_id && entries[i].fold_key === fold_key) {
      entries[i] = { ...entries[i], repeat_count }
      return true
    }
  }
  return false
}
