import type { LogEntry } from '@/api/agent'

/** Client-side fields added during ingest (not from agent API). */
export interface DisplayLogEntry extends LogEntry {
  normalized_message: string
  repeat_count: number
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

export function toDisplayEntry(log: LogEntry): DisplayLogEntry {
  return {
    ...log,
    normalized_message: normalize(log.message),
    repeat_count: log.repeat_count ?? 1,
  }
}

/** Fold adjacent duplicates (same service_id + normalized_message) into last row. */
export function ingest(entry: DisplayLogEntry, entries: DisplayLogEntry[]): void {
  const last = entries[entries.length - 1]
  if (
    last &&
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
