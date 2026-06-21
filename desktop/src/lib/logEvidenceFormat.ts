// logEvidenceFormat 提供日志证据包的纯格式化和区间建模能力。
//
// 职责：
//   - 统一 cursor 元数据格式
//   - 构建同轨道证据区间和跨轨道全局时间线
//   - 输出 Agent 可读的 Markdown
//
// 边界：
//   - 不持有 Pinia 状态
//   - 不访问剪贴板、文件系统或 DOM
import type { LogEntry } from '@/api/agent'
import type { EvidencePin } from '@/stores/logEvidence'

export interface EvidenceSegment {
  key: string
  trackId: string
  trackLabel: string
  from: EvidencePin
  to: EvidencePin
  logs: LogEntry[]
  skipped: boolean
}

export interface EvidenceTrackExport {
  trackId: string
  trackLabel: string
  pins: EvidencePin[]
  segments: EvidenceSegment[]
}

export interface EvidenceExportModel {
  pins: EvidencePin[]
  tracks: EvidenceTrackExport[]
  timeline: EvidencePin[]
}

export interface BuildEvidenceExportModelInput {
  pins: EvidencePin[]
  logsByTrack: Record<string, LogEntry[]>
  skippedSegmentKeys: Set<string>
}

function cursorMs(log: Pick<LogEntry, 'timestamp'>): number {
  const ms = new Date(log.timestamp).getTime()
  return Number.isFinite(ms) ? ms : 0
}

/**
 * compareLogCursors 按 Agent 查询 cursor 顺序比较两条日志。
 *
 * 参数：
 *   - a: 第一条日志
 *   - b: 第二条日志
 *
 * 返回：
 *   - 负数表示 a 在前，正数表示 b 在前，0 表示同一 cursor
 *
 * 注意：
 *   - timestamp 相同时使用字符串化 id 做稳定排序，和后端 cursor_id 语义保持一致
 */
export function compareLogCursors(a: Pick<LogEntry, 'timestamp' | 'id'>, b: Pick<LogEntry, 'timestamp' | 'id'>): number {
  const timeDiff = cursorMs(a) - cursorMs(b)
  if (timeDiff !== 0) return timeDiff
  return String(a.id).localeCompare(String(b.id), undefined, { numeric: true })
}

export function comparePinsByCursor(a: EvidencePin, b: EvidencePin): number {
  return compareLogCursors(a.log, b.log)
}

function formatRepeatCount(log: LogEntry): string {
  return log.repeat_count && log.repeat_count > 1 ? `\nrepeat_count: ${log.repeat_count}` : ''
}

/**
 * formatLogWithCursor 输出单条日志及 Agent 查询所需 cursor。
 */
export function formatLogWithCursor(log: LogEntry): string {
  return [
    `deployment_id: ${log.deployment_id}`,
    `cursor_time: ${log.timestamp}`,
    `cursor_id: ${log.id}`,
    `source_id: ${log.source_id ?? ''}`,
    `level: ${log.level}`,
    `stream: ${log.stream}`,
    `message: ${log.message}${formatRepeatCount(log)}`,
  ].join('\n')
}

function formatLogText(log: LogEntry): string {
  const repeat = Math.max(1, log.repeat_count ?? 1)
  const line = `${log.timestamp} [${log.deployment_id}] ${log.level.padEnd(5)} ${log.message}`
  return Array.from({ length: repeat }, () => line).join('\n')
}

function formatPin(pin: EvidencePin): string {
  const note = pin.note.trim() ? pin.note.trim() : '(empty)'
  return [
    `### ${pin.label}`,
    `- color: ${pin.color}`,
    `- track: ${pin.trackLabel}`,
    `- deployment_id: ${pin.log.deployment_id}`,
    `- cursor_time: ${pin.log.timestamp}`,
    `- cursor_id: ${pin.log.id}`,
    `- source_id: ${pin.log.source_id ?? ''}`,
    `- level: ${pin.log.level}`,
    `- stream: ${pin.log.stream}`,
    `- note: ${note}`,
    '',
    '```text',
    formatLogText(pin.log),
    '```',
  ].join('\n')
}

/**
 * formatPinnedLinesMarkdown 只导出被打钉的日志快照。
 */
export function formatPinnedLinesMarkdown(pins: EvidencePin[]): string {
  const sortedPins = [...pins].sort(comparePinsByCursor)
  return ['# SuperDev Log Evidence', '', '## Pins', '', ...sortedPins.map(formatPin)].join('\n')
}

function segmentKey(trackId: string, from: EvidencePin, to: EvidencePin): string {
  return `${trackId}:${from.log.id}:${to.log.id}`
}

function logsBetween(logs: LogEntry[], from: EvidencePin, to: EvidencePin): LogEntry[] {
  return logs
    .filter(log => compareLogCursors(log, from.log) >= 0 && compareLogCursors(log, to.log) <= 0)
    .sort(compareLogCursors)
}

/**
 * buildEvidenceExportModel 将 pins 转为导出模型。
 *
 * 注意：
 *   - 只在同 track 内生成区间；跨 track pins 只进入 timeline
 *   - skippedSegmentKeys 使用稳定 key，便于 drawer 反复预览
 */
export function buildEvidenceExportModel(input: BuildEvidenceExportModelInput): EvidenceExportModel {
  const pins = [...input.pins].sort(comparePinsByCursor)
  const byTrack = new Map<string, EvidencePin[]>()
  for (const pin of pins) {
    const list = byTrack.get(pin.trackId) ?? []
    list.push(pin)
    byTrack.set(pin.trackId, list)
  }

  const tracks: EvidenceTrackExport[] = []
  for (const [trackId, trackPins] of byTrack) {
    const sortedTrackPins = [...trackPins].sort(comparePinsByCursor)
    const segments: EvidenceSegment[] = []
    for (let index = 0; index < sortedTrackPins.length - 1; index++) {
      const from = sortedTrackPins[index]
      const to = sortedTrackPins[index + 1]
      const key = segmentKey(trackId, from, to)
      segments.push({
        key,
        trackId,
        trackLabel: from.trackLabel,
        from,
        to,
        logs: logsBetween(input.logsByTrack[trackId] ?? [], from, to),
        skipped: input.skippedSegmentKeys.has(key),
      })
    }
    tracks.push({
      trackId,
      trackLabel: sortedTrackPins[0]?.trackLabel ?? trackId,
      pins: sortedTrackPins,
      segments,
    })
  }

  tracks.sort((a, b) => a.trackLabel.localeCompare(b.trackLabel))
  return { pins, tracks, timeline: pins }
}

function formatSegment(segment: EvidenceSegment): string {
  const title = segment.skipped
    ? `### Omitted ${segment.from.label} -> ${segment.to.label}`
    : `### Segment ${segment.from.label} -> ${segment.to.label}`
  const header = [
    title,
    `- from_cursor_time: ${segment.from.log.timestamp}`,
    `- from_cursor_id: ${segment.from.log.id}`,
    `- to_cursor_time: ${segment.to.log.timestamp}`,
    `- to_cursor_id: ${segment.to.log.id}`,
  ]
  if (segment.skipped) {
    return [...header, '- reason: user skipped this interval'].join('\n')
  }
  return [
    ...header,
    '',
    '```text',
    segment.logs.map(formatLogText).join('\n'),
    '```',
  ].join('\n')
}

/**
 * formatEvidenceMarkdown 输出完整证据包 Markdown。
 */
export function formatEvidenceMarkdown(model: EvidenceExportModel): string {
  const parts: string[] = ['# SuperDev Log Evidence', '', '## Pins', '']
  parts.push(...model.pins.map(formatPin))
  parts.push('', '## Segments', '')
  for (const track of model.tracks) {
    parts.push(`### Track: ${track.trackLabel}`, '')
    if (track.segments.length === 0) {
      parts.push('- No same-track intervals', '')
      continue
    }
    parts.push(...track.segments.map(formatSegment), '')
  }
  parts.push('## Timeline', '')
  for (const pin of model.timeline) {
    const note = pin.note.trim() ? ` note: ${pin.note.trim()}` : ''
    parts.push(`- ${pin.label} ${pin.trackLabel} ${pin.log.timestamp}${note}`)
  }
  return parts.join('\n').trimEnd()
}

/**
 * nearestLogIndexByCursorTime 返回最接近目标 cursor_time 的已加载日志下标。
 */
export function nearestLogIndexByCursorTime(logs: LogEntry[], cursorTime: string, cursorId = ''): number {
  if (logs.length === 0) return -1
  const target = { timestamp: cursorTime, id: cursorId }
  let bestIndex = 0
  let bestScore = Number.POSITIVE_INFINITY
  logs.forEach((log, index) => {
    const idDistance = Math.abs(String(log.id).localeCompare(String(cursorId), undefined, { numeric: true }))
    const score = Math.abs(cursorMs(log) - cursorMs(target)) * 1000 + idDistance
    if (score < bestScore) {
      bestScore = score
      bestIndex = index
    }
  })
  return bestIndex
}
