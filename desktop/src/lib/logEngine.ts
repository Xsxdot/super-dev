import type { LogEntry } from '@/api/agent'

const SYNTHETIC_LOG_ID_START = 1_000_000_000_000
let nextSyntheticLogId = SYNTHETIC_LOG_ID_START

/** Client-side fields added during ingest (not from agent API). */
export interface DisplayLogEntry extends LogEntry {
  normalized_message: string
  repeat_count: number
  fold_closed?: boolean
  _sig?: string     // original signature for seenSignatures cleanup, stored before ingest mutates timestamp
  _allSigs?: string[] // all fold-merged signatures (including duplicates) for complete seenSignatures cleanup
}

export function normalize(line: string): string {
  let result = line

  // Strip leading HH:MM:SS[.fff]
  result = result.replace(/^\d{2}:\d{2}:\d{2}(\.\d+)?\s*/, '')

  // Strip leading ISO date-time prefix
  result = result.replace(/^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?\s*/, '')

  // Strip weekday date prefix (e.g. "Wed May 20 17:20:51 CST 2026")
  result = result.replace(
    /^[A-Z][a-z]{2}\s+[A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\s+[A-Z]{2,4}\s+\d{4}\s*/,
    '',
  )

  // key=123 → key=*
  result = result.replace(/=\d+/g, '=*')

  // IPv4:port → *:*
  result = result.replace(/\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+/g, '*:*')

  return result.trim()
}

function displayLogId(log: LogEntry): number {
  if (log.id > 0) return log.id
  // 实时 WebSocket 日志尚未写入 SQLite，后端 id 为 0；前端必须补稳定 id 供渲染和书签去重使用。
  return nextSyntheticLogId++
}

/**
 * toDisplayEntry 将 API 日志转换为前端可渲染日志。
 *
 * 参数：
 *   - log: agent 返回或实时推送的原始日志
 *
 * 返回：
 *   - 带规范化消息、重复计数和稳定显示 id 的日志
 *
 * 注意：
 *   - 实时日志可能没有数据库 id，需要在前端分配合成 id，避免书签捕获互相覆盖
 */
export function toDisplayEntry(log: LogEntry): DisplayLogEntry {
  return {
    ...log,
    id: displayLogId(log),
    normalized_message: normalize(log.message),
    repeat_count: log.repeat_count ?? 1,
  }
}

/** Fold adjacent duplicates (same service_id + normalized_message) into last row. */
export function ingest(entry: DisplayLogEntry, entries: DisplayLogEntry[]): void {
  const last = entries[entries.length - 1]
  if (
    last &&
    !last.fold_closed &&
    last.service_id === entry.service_id &&
    last.normalized_message === entry.normalized_message
  ) {
    last.repeat_count += 1
    last.timestamp = entry.timestamp
    entries[entries.length - 1] = last
  } else {
    entries.push(entry)
  }
}

/** Close the current folded row so the next duplicate starts a new row. */
export function closeActiveFold(entries: DisplayLogEntry[]): void {
  const last = entries[entries.length - 1]
  if (last) last.fold_closed = true
}
