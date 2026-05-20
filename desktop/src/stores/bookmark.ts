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

export const useBookmarkStore = defineStore('bookmark', () => {
  const bookmarks = ref<Record<string, Bookmark>>({})
  // 同步组：panelId 集合
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

  function endBookmark(panelId: string) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    bm.endTime = new Date()
    bm.state = 'done'
  }

  function clearBookmark(panelId: string) {
    delete bookmarks.value[panelId]
  }

  // 录制过程中追加过滤后的日志
  function appendToBookmark(panelId: string, log: LogEntry) {
    const bm = bookmarks.value[panelId]
    if (!bm || bm.state !== 'recording') return
    if (!bm.startTime || new Date(log.timestamp) < bm.startTime) return
    bm.lockedLogs.push(log)
  }

  // 格式化书签日志为可导出文本
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

  // 格式化同步组所有书签（按服务分块）
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

  // 同步组操作
  function toggleSyncPanel(panelId: string, serviceId: string | null) {
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
        serviceId: null,  // 由面板组件填入实际 serviceId
        state: 'recording',
        startTime: now,  // 所有面板使用同一时间戳，保证对齐
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
    appendToBookmark,
    formatBookmark,
    formatSyncBookmarks,
    toggleSyncPanel,
    startSyncBookmark,
    endSyncBookmark,
  }
})
