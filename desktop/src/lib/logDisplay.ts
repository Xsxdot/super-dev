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
import type { MirrorEvent } from '@/stores/portMirror'

export type LogDisplayItem =
  | { kind: 'entry'; id: string; log: DisplayLogEntry }
  | { kind: 'markerStart'; id: string; date: Date }
  | { kind: 'markerEnd'; id: string; date: Date }
  | { kind: 'historySeparator'; id: string }
  | { kind: 'lifecycleSeparator'; id: string; marker: LogLifecycleMarker }
  | { kind: 'gapSeparator'; id: string; time: string }
  // mirrorEvent 语义边界对齐 lifecycleSeparator：纯展示行，不进日志数组、不参与
  // 过滤/导出/搜索/证据钉、不持久化，只出现在 makeDisplayItems 的输出里。
  // 为什么不伪造 LogEntry：会污染导出/证据钉且需凑 run_id 等必填字段（Global Constraints 偏离 2）。
  | { kind: 'mirrorEvent'; id: string; at: number; port: number; hostName: string;
      event: 'established' | 'failed' | 'conflict' | 'removed' }

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
  id: string
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

// withGapSeparators 把断流缺口标记按时间插入显示列表。
// 与 lifecycle marker 同构；缺口行提示"此处可能缺失日志"，是补拉封顶/失败的可见认账。
function withGapSeparators(
  items: LogDisplayItem[],
  markers: { id: string; time: string }[] = [],
): LogDisplayItem[] {
  if (!markers.length) return items
  const out = [...items]
  for (const marker of markers) {
    const markerTime = new Date(marker.time).getTime()
    const insertAt = out.findIndex(item =>
      item.kind === 'entry' && new Date(item.log.timestamp).getTime() > markerTime
    )
    const displayItem: LogDisplayItem = { kind: 'gapSeparator', id: `gap-${marker.id}`, time: marker.time }
    if (insertAt < 0) out.push(displayItem)
    else out.splice(insertAt, 0, displayItem)
  }
  return out
}

// withMirrorEvents 把端口镜像事件（MirrorEvent）按时间插入显示列表。
//
// 与 withLifecycleSeparators 同构，包括先排序再插入这一步——插入位置只看"下一条
// entry 的时间"，如果两个事件都落在同一段 entry 间隙里，不按时间排序就处理会导致
// 后处理的事件被插在先处理的事件前面（顺序倒挂）。先按 at 升序排序，就能保证每条
// 事件相对之前已插入的事件而言，也一定落在正确的时间位置上。
function withMirrorEvents(
  items: LogDisplayItem[],
  events: MirrorEvent[] = [],
): LogDisplayItem[] {
  if (!events.length) return items
  const out = [...items]
  const sorted = [...events].sort((a, b) => a.at - b.at)
  for (const event of sorted) {
    const insertAt = out.findIndex(item =>
      item.kind === 'entry' && new Date(item.log.timestamp).getTime() > event.at
    )
    const displayItem: LogDisplayItem = {
      kind: 'mirrorEvent',
      // id 必须带 hostName：一个 deployment 可能有多个副本跑在不同 host 上，
      // 同端口同时跃迁到同一 kind 时 diffSnapshots 常在同一毫秒内产出多条事件
      // （见 portMirror.ts diffSnapshots，同步循环里逐条 Date.now()）。缺了
      // host 判别符会让两条本质不同的事件撞成同一个 id，破坏虚拟列表
      // getItemKey 的唯一性——这是 store 自己的 mirrorEntryKey（同文件
      // host_id::deployment_id::port 三元组）已经踩过并修复的同一个坑。
      id: `mirror-${event.deploymentId}-${event.hostName}-${event.port}-${event.kind}-${event.at}`,
      at: event.at,
      port: event.port,
      hostName: event.hostName,
      event: event.kind,
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
 *   - mirrorEvents 同 lifecycleMarkers/gapMarkers：纯展示插入，不影响 logs 本身
 */
export function makeDisplayItems(
  logs: DisplayLogEntry[],
  bm: BookmarkDisplayInput | null,
  markerIds: MarkerIds,
  historyBoundary: HistoryBoundary | null = null,
  lifecycleMarkers: LogLifecycleMarker[] = [],
  gapMarkers: { id: string; time: string }[] = [],
  mirrorEvents: MirrorEvent[] = [],
): LogDisplayItem[] {
  const items: LogDisplayItem[] = []
  if (!bm?.startTime) {
    for (const log of logs) items.push(entryItem(log))
    return withMirrorEvents(
      withGapSeparators(
        withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers),
        gapMarkers,
      ),
      mirrorEvents,
    )
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
    return withMirrorEvents(
      withGapSeparators(
        withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers),
        gapMarkers,
      ),
      mirrorEvents,
    )
  }

  const before = logs.filter(l => ts(l) < startTime)
  const after = logs.filter(l => ts(l) >= startTime)
  for (const log of before) items.push(entryItem(log))
  if ((after.length > 0 || bm.state === 'recording') && markerIds.start) {
    items.push({ kind: 'markerStart', id: markerIds.start, date: startTime })
  }
  for (const log of after) items.push(entryItem(log))
  return withMirrorEvents(
    withGapSeparators(
      withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers),
      gapMarkers,
    ),
    mirrorEvents,
  )
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
