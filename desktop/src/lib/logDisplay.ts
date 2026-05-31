// 日志显示列表构造：把实时日志、书签冻结快照和标记行组合成面板可渲染的线性列表。
//
// 职责：
//   - 根据书签状态插入开始/结束标记
//   - 保持实时日志流完整可见，书签只负责框定区间
//   - 统计当前显示列表中的日志和折叠数量
//
// 边界：
//   - 不读取或修改日志 store，仅处理传入的数据
//   - 不负责日志折叠规则，折叠由 logEngine 完成
import type { LogEntry } from '@/api/agent'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { BookmarkState } from '@/stores/bookmark'
import type { LogLifecycleMarker } from '@/stores/logLifecycle'

export type LogDisplayItem =
  | { kind: 'entry'; id: string; log: DisplayLogEntry }
  | { kind: 'markerStart'; id: string; date: Date }
  | { kind: 'markerEnd'; id: string; date: Date }
  | { kind: 'historySeparator'; id: string }
  | { kind: 'lifecycleSeparator'; id: string; marker: LogLifecycleMarker }

export interface BookmarkDisplayInput {
  state: BookmarkState
  startTime: Date
  endTime: Date | null
  lockedLogs?: LogEntry[]
}

export interface MarkerIds {
  start: string
  end: string
}

export interface HistoryBoundary {
  timestamp: string
  id: number
}

function ts(log: DisplayLogEntry): Date {
  return new Date(log.timestamp)
}

function entryItem(log: DisplayLogEntry, scope = 'live'): LogDisplayItem {
  return { kind: 'entry', id: `${scope}-${log.id}`, log }
}

function isAtOrBeforeBoundary(log: DisplayLogEntry, boundary: HistoryBoundary): boolean {
  const diff = new Date(log.timestamp).getTime() - new Date(boundary.timestamp).getTime()
  return diff < 0 || (diff === 0 && log.id <= boundary.id)
}

function withHistorySeparator(items: LogDisplayItem[], boundary: HistoryBoundary | null): LogDisplayItem[] {
  if (!boundary) return items
  let insertAfter = -1
  for (let i = 0; i < items.length; i++) {
    const item = items[i]
    if (item.kind === 'entry' && isAtOrBeforeBoundary(item.log, boundary)) {
      insertAfter = i
    }
  }
  if (insertAfter < 0) return items
  return [
    ...items.slice(0, insertAfter + 1),
    { kind: 'historySeparator', id: `history-separator-${boundary.timestamp}-${boundary.id}` },
    ...items.slice(insertAfter + 1),
  ]
}

function withLifecycleSeparators(
  items: LogDisplayItem[],
  markers: LogLifecycleMarker[] = [],
): LogDisplayItem[] {
  if (!markers.length) return items
  const out = [...items]
  const sorted = [...markers].sort(
    (a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime(),
  )
  for (const marker of sorted) {
    const markerTime = new Date(marker.createdAt).getTime()
    const insertAt = out.findIndex(item =>
      item.kind === 'entry' && new Date(item.log.timestamp).getTime() > markerTime
    )
    const displayItem: LogDisplayItem = {
      kind: 'lifecycleSeparator',
      id: `lifecycle-${marker.id}`,
      marker,
    }
    if (insertAt < 0) out.push(displayItem)
    else out.splice(insertAt, 0, displayItem)
  }
  return out
}

/**
 * makeDisplayItems 构造带书签标记的日志显示列表。
 *
 * 参数：
 *   - logs: 当前实时/历史日志显示行
 *   - bm: 当前书签显示状态，done 状态可携带冻结快照
 *   - markerIds: 标记行稳定 id
 *
 * 返回：
 *   - 可直接渲染的日志行和标记行列表
 *
 * 注意：
 *   - lockedLogs 仅用于复制/导出，显示层不使用它替换 live 日志流
 */
export function makeDisplayItems(
  logs: DisplayLogEntry[],
  bm: BookmarkDisplayInput | null,
  markerIds: MarkerIds,
  historyBoundary: HistoryBoundary | null = null,
  lifecycleMarkers: LogLifecycleMarker[] = [],
): LogDisplayItem[] {
  const items: LogDisplayItem[] = []
  if (!bm?.startTime) {
    for (const log of logs) items.push(entryItem(log))
    return withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers)
  }

  const startTime = bm.startTime

  if (bm.state === 'done') {
    const endTime = bm.endTime ?? new Date()
    const before = logs.filter(l => ts(l) < startTime)
    const inRange = logs.filter(l => {
      const t = ts(l)
      return t >= startTime && t <= endTime
    })
    const after = logs.filter(l => ts(l) > endTime)

    for (const log of before) items.push(entryItem(log))
    if (markerIds.start) {
      items.push({ kind: 'markerStart', id: markerIds.start, date: startTime })
    }
    for (const log of inRange) items.push(entryItem(log, 'locked'))
    if (markerIds.end) {
      items.push({ kind: 'markerEnd', id: markerIds.end, date: endTime })
    }
    for (const log of after) items.push(entryItem(log))
    return withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers)
  }

  const before = logs.filter(l => ts(l) < startTime)
  const after = logs.filter(l => ts(l) >= startTime)
  for (const log of before) items.push(entryItem(log))
  if ((after.length > 0 || bm.state === 'recording') && markerIds.start) {
    items.push({ kind: 'markerStart', id: markerIds.start, date: startTime })
  }
  for (const log of after) items.push(entryItem(log))
  return withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers)
}

export interface DisplayStats {
  total: number
  folded: number
  errors: number
  warns: number
}

/**
 * computeDisplayStats 统计当前显示列表中的日志指标。
 *
 * 参数：
 *   - items: makeDisplayItems 生成的显示项列表
 *
 * 返回：
 *   - total: 当前显示的日志行数量
 *   - folded: 被折叠隐藏的重复日志数量
 *   - errors: ERROR 行数量
 *   - warns: WARN 行数量
 *
 * 注意：
 *   - marker 行不参与统计
 */
export function computeDisplayStats(items: LogDisplayItem[]): DisplayStats {
  let folded = 0
  let errors = 0
  let warns = 0
  let total = 0
  for (const item of items) {
    if (item.kind !== 'entry') continue
    total++
    const e = item.log
    const rc = e.repeat_count ?? 1
    if (rc > 1) folded += rc - 1
    if (e.level === 'ERROR') errors++
    else if (e.level === 'WARN') warns++
  }
  return { total, folded, errors, warns }
}
