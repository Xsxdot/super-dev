// bookmarkStore 按 panelId 维护书签状态，支持单面板独立录制和多面板同步录制。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { LogEntry } from '@/api/agent'

export type BookmarkState = 'idle' | 'recording' | 'done'

export interface Bookmark {
  panelId: string
  serviceId: string | null
  state: BookmarkState
  startTime: Date | null
  endTime: Date | null
  lockedLogs: LogEntry[]
}

function snapshotLog(log: LogEntry): LogEntry {
  return { ...log }
}

function dedupeById(logs: LogEntry[]): LogEntry[] {
  const seen = new Set<number>()
  const out: LogEntry[] = []
  for (const log of logs) {
    if (seen.has(log.id)) continue
    seen.add(log.id)
    out.push(snapshotLog(log))
  }
  return out
}

export const useBookmarkStore = defineStore('bookmark', () => {
  const bookmarks = ref<Record<string, Bookmark>>({})
  const syncPanelIds = ref<Set<string>>(new Set())
  const syncRecording = ref(false)

  function getBookmark(panelId: string): Bookmark | null {
    return bookmarks.value[panelId] ?? null
  }

  function startBookmark(panelId: string, serviceId: string | null) {
    bookmarks.value[panelId] = {
      panelId,
      serviceId,
      state: 'recording',
      startTime: new Date(),
      endTime: null,
      lockedLogs: [],
    }
  }

  function endBookmark(panelId: string, captureLogs?: LogEntry[]) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    bm.endTime = new Date()
    if (captureLogs && bm.startTime) {
      const start = bm.startTime
      const inRange = captureLogs.filter(l => new Date(l.timestamp) >= start)
      bm.lockedLogs = dedupeById(inRange)
    }
    bm.state = 'done'
  }

  function clearBookmark(panelId: string) {
    delete bookmarks.value[panelId]
  }

  function finalizeLockedLogs(panelId: string, captureLogs: LogEntry[]) {
    const bm = bookmarks.value[panelId]
    if (!bm?.startTime || !bm.endTime || bm.lockedLogs.length > 0) return
    const start = bm.startTime
    bm.lockedLogs = dedupeById(
      captureLogs.filter(l => new Date(l.timestamp) >= start),
    )
  }

  function appendToBookmark(panelId: string, log: LogEntry) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    if (!bm.startTime || new Date(log.timestamp) < bm.startTime) return
    if (bm.lockedLogs.some(l => l.id === log.id)) return
    bm.lockedLogs.push(snapshotLog(log))
  }

  function formatBookmark(panelId: string): string {
    const bm = bookmarks.value[panelId]
    if (!bm) return ''
    return bm.lockedLogs
      .map(l => {
        const t = new Date(l.timestamp).toLocaleTimeString('en-US', { hour12: false })
        return `${t} [${l.service_id}] ${l.level.padEnd(5)} ${l.message}`
      })
      .join('\n')
  }

  function formatSyncBookmarks(): string {
    const parts: string[] = []
    for (const panelId of syncPanelIds.value) {
      const bm = bookmarks.value[panelId]
      if (!bm) continue
      const header = `=== ${bm.serviceId ?? 'unknown'} ===`
      const body = bm.lockedLogs
        .map(l => {
          const t = new Date(l.timestamp).toLocaleTimeString('en-US', { hour12: false })
          return `${t} [${l.service_id}] ${l.level.padEnd(5)} ${l.message}`
        })
        .join('\n')
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

  function startSyncBookmark() {
    const now = new Date()
    for (const panelId of syncPanelIds.value) {
      bookmarks.value[panelId] = {
        panelId,
        serviceId: null,
        state: 'recording',
        startTime: now,
        endTime: null,
        lockedLogs: [],
      }
    }
    syncRecording.value = true
  }

  function endSyncBookmark() {
    const now = new Date()
    for (const panelId of syncPanelIds.value) {
      const bm = bookmarks.value[panelId]
      if (bm && bm.state === 'recording') {
        bm.endTime = now
        bm.state = 'done'
      }
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
