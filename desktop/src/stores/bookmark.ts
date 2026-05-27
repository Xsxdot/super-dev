// bookmarkStore 按 panelId 维护书签状态，支持单面板独立录制和多面板同步录制。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { LogEntry } from '@/api/agent'
import type { PanelSource } from './panel'

export type BookmarkState = 'idle' | 'recording' | 'done'

export interface Bookmark {
  panelId: string
  serviceId: string | null
  source?: PanelSource | null
  state: BookmarkState
  startTime: Date | null
  endTime: Date | null
  lockedLogs: LogEntry[]
}

export interface SyncBookmarkPanel {
  panelId: string
  serviceId: string | null
  source?: PanelSource | null
}

export interface SyncBookmarkCapture {
  panelId: string
  captureLogs?: LogEntry[]
  capturedIds?: Iterable<number>
}

function snapshotLog(log: LogEntry): LogEntry {
  return { ...log }
}

function logsInBookmarkRange(log: LogEntry, start: Date, end: Date): boolean {
  const t = new Date(log.timestamp)
  return t >= start && t <= end
}

/**
 * captureLockedLogs 在结束标记时冻结书签区间快照。
 *
 * 参数：
 *   - captureLogs: 当前可见日志快照
 *   - start: 开始标记时间
 *   - end: 结束标记时间
 *   - capturedIds: 录制期间见过的日志 id，折叠行 timestamp 后移时用于保留该行
 *   - recordedLogs: 录制过程中已捕获的快照，结束快照缺失时用于兜底
 *
 * 返回：
 *   - 去重并按时间排序后的书签日志快照
 *
 * 注意：
 *   - 折叠行会复用同一个 id 并更新 timestamp，所以不能只依赖 end 前时间范围筛选
 */
export function captureLockedLogs(
  captureLogs: LogEntry[],
  start: Date,
  end: Date,
  capturedIds?: Iterable<number>,
  recordedLogs: LogEntry[] = [],
): LogEntry[] {
  const byId = new Map(recordedLogs.map(l => [l.id, l]))
  for (const log of captureLogs) {
    byId.set(log.id, log)
  }
  const seen = new Set<number>()
  const out: LogEntry[] = []

  const add = (log: LogEntry) => {
    if (seen.has(log.id)) return
    seen.add(log.id)
    out.push(snapshotLog(log))
  }

  if (capturedIds) {
    for (const id of capturedIds) {
      const log = byId.get(id)
      if (log) add(log)
    }
  }
  for (const log of captureLogs) {
    if (logsInBookmarkRange(log, start, end)) add(log)
  }
  for (const log of recordedLogs) {
    // 结束瞬间的可见快照可能缺少早前录到的行，必须保留录制期快照。
    if (logsInBookmarkRange(log, start, end)) add(log)
  }

  return out.sort(
    (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
  )
}

function sourceLabel(bm: Bookmark): string {
  if (bm.source?.type === 'local-service') return bm.source.serviceId
  if (bm.source?.type === 'local-project') return `${bm.source.projectId} · all`
  return bm.serviceId ?? 'unknown'
}

function formatLogLines(l: LogEntry): string[] {
  const t = new Date(l.timestamp).toLocaleTimeString('en-US', { hour12: false })
  const base = `${t} [${l.service_id}] ${l.level.padEnd(5)} ${l.message}`
  const n = l.repeat_count ?? 1
  return Array.from({ length: Math.max(1, n) }, () => base)
}

export const useBookmarkStore = defineStore('bookmark', () => {
  const bookmarks = ref<Record<string, Bookmark>>({})
  const syncPanelIds = ref<Set<string>>(new Set())
  const syncRecording = ref(false)

  function getBookmark(panelId: string): Bookmark | null {
    return bookmarks.value[panelId] ?? null
  }

  function startBookmark(panelId: string, serviceId: string | null, source: PanelSource | null = null) {
    bookmarks.value[panelId] = {
      panelId,
      serviceId,
      source,
      state: 'recording',
      startTime: new Date(),
      endTime: null,
      lockedLogs: [],
    }
  }

  function endBookmark(
    panelId: string,
    captureLogs?: LogEntry[],
    capturedIds?: Iterable<number>,
  ) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    bm.endTime = new Date()
    if (captureLogs && bm.startTime && bm.endTime) {
      const recordedLogs = bm.lockedLogs
      bm.lockedLogs = captureLockedLogs(
        captureLogs,
        bm.startTime,
        bm.endTime,
        capturedIds,
        recordedLogs,
      )
    }
    bm.state = 'done'
  }

  function clearBookmark(panelId: string) {
    delete bookmarks.value[panelId]
  }

  function finalizeLockedLogs(
    panelId: string,
    captureLogs: LogEntry[],
    capturedIds?: Iterable<number>,
  ) {
    const bm = bookmarks.value[panelId]
    if (!bm?.startTime || !bm.endTime || bm.lockedLogs.length > 0) return
    bm.lockedLogs = captureLockedLogs(
      captureLogs,
      bm.startTime,
      bm.endTime,
      capturedIds,
    )
  }

  function appendToBookmark(panelId: string, log: LogEntry) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    if (!bm.startTime || new Date(log.timestamp) < bm.startTime) return
    const snap = snapshotLog(log)
    const idx = bm.lockedLogs.findIndex(l => l.id === log.id)
    if (idx >= 0) bm.lockedLogs[idx] = snap
    else bm.lockedLogs.push(snap)
  }

  function formatBookmark(panelId: string): string {
    const bm = bookmarks.value[panelId]
    if (!bm) return ''
    return bm.lockedLogs.flatMap(formatLogLines).join('\n')
  }

  function formatSyncBookmarks(): string {
    const parts: string[] = []
    for (const panelId of syncPanelIds.value) {
      const bm = bookmarks.value[panelId]
      if (!bm) continue
      const header = `=== ${sourceLabel(bm)} ===`
      const body = bm.lockedLogs.flatMap(formatLogLines).join('\n')
      parts.push(`${header}\n${body}`)
    }
    return parts.join('\n\n')
  }

  function toggleSyncPanel(panelId: string, _serviceId: string | null) {
    if (syncPanelIds.value.has(panelId)) {
      syncPanelIds.value.delete(panelId)
    } else {
      syncPanelIds.value.add(panelId)
    }
  }

  function startSyncBookmark(panels?: SyncBookmarkPanel[]) {
    const now = new Date()
    const targets: SyncBookmarkPanel[] = panels ?? [...syncPanelIds.value].map(panelId => ({ panelId, serviceId: null, source: null }))
    for (const target of targets) {
      syncPanelIds.value.add(target.panelId)
      bookmarks.value[target.panelId] = {
        panelId: target.panelId,
        serviceId: target.serviceId,
        source: target.source ?? null,
        state: 'recording',
        startTime: now,
        endTime: null,
        lockedLogs: [],
      }
    }
    syncRecording.value = true
  }

  function endSyncBookmark(captures: SyncBookmarkCapture[] = []) {
    const byPanelId = new Map(captures.map(capture => [capture.panelId, capture]))
    for (const panelId of syncPanelIds.value) {
      const capture = byPanelId.get(panelId)
      endBookmark(panelId, capture?.captureLogs, capture?.capturedIds)
    }
    syncRecording.value = false
  }

  return {
    bookmarks,
    syncPanelIds,
    syncRecording,
    getBookmark,
    startBookmark,
    endBookmark,
    clearBookmark,
    finalizeLockedLogs,
    appendToBookmark,
    formatBookmark,
    formatSyncBookmarks,
    toggleSyncPanel,
    startSyncBookmark,
    endSyncBookmark,
  }
})
