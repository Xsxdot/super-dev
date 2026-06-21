// logEvidence store 管理日志证据钉子的会话态、跨分栏轨道和导出模型。
//
// 职责：
//   - 添加、取消、备注和查询证据钉子
//   - 管理证据抽屉范围、同轨道区间和跨轨道时间线
//   - 注册 LogPanel 轨道控制器，用于跳转和时间同步
//
// 边界：
//   - 不持久化到 localStorage
//   - 不直接渲染 UI
//   - 不访问后端 API
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import type { LogEntry } from '@/api/agent'
import {
  buildEvidenceExportModel,
  comparePinsByCursor,
  formatEvidenceMarkdown,
  formatPinnedLinesMarkdown,
  nearestLogIndexByCursorTime,
} from '@/lib/logEvidenceFormat'
import { logEvidenceDiagnostic } from '@/lib/logEvidenceDiagnostics'

export const EVIDENCE_PIN_COLORS = ['#58a6ff', '#f5a524', '#36cfc9', '#9254de', '#52c41a', '#f759ab', '#13c2c2', '#fa541c']
export const SAME_TIME_CANDIDATE_WINDOW_MS = 5000

export type EvidenceScopeMode = 'current' | 'selected' | 'all'

export interface EvidencePin {
  id: string
  panelId: string
  trackId: string
  trackLabel: string
  sourceKey: string
  sequence: number
  label: string
  color: string
  logId: string
  log: LogEntry
  note: string
  createdAt: string
}

export interface AddEvidencePinInput {
  panelId: string
  trackId: string
  trackLabel: string
  sourceKey: string
  log: LogEntry
}

export interface TogglePinResult {
  action: 'added' | 'removed'
  pin: EvidencePin
}

export interface EvidenceTrackRegistration {
  trackId: string
  panelId: string
  trackLabel: string
  sourceKey: string
  getLogs: () => LogEntry[]
  jumpToLog: (logId: string) => Promise<boolean> | boolean
  alignToTime: (cursorTime: string, cursorId: string) => Promise<boolean> | boolean
}

export interface SameTimePinCandidate {
  trackId: string
  trackLabel: string
  sourceKey: string
  log: LogEntry
  distanceMs: number
}

function snapshotLog(log: LogEntry): LogEntry {
  return { ...log, id: String(log.id ?? '') }
}

function pinIdentity(input: Pick<AddEvidencePinInput, 'trackId' | 'sourceKey' | 'log'>): string {
  return `${input.trackId}:${input.sourceKey}:${String(input.log.id ?? '')}`
}

function ms(timestamp: string): number {
  const value = new Date(timestamp).getTime()
  return Number.isFinite(value) ? value : 0
}

function makePinId(): string {
  return crypto.randomUUID()
}

/**
 * useLogEvidenceStore 提供日志证据钉子的会话态操作。
 *
 * 返回：
 *   - pins、scope、time sync、track registry 和 Markdown 格式化方法
 *
 * 注意：
 *   - 该 store 只保存当前前端会话状态，不做持久化
 *   - 区间导出只在同 track 内生成，跨 track pins 仅进入全局 timeline
 */
export const useLogEvidenceStore = defineStore('logEvidence', () => {
  const pins = ref<EvidencePin[]>([])
  const drawerOpen = ref(false)
  const scopeMode = ref<EvidenceScopeMode>('all')
  const currentTrackId = ref<string | null>(null)
  const selectedTrackIds = ref<Set<string>>(new Set())
  const skippedSegmentKeys = ref<Set<string>>(new Set())
  const timeSyncEnabled = ref(false)
  const activeTimeAnchor = ref<{ trackId: string; cursorTime: string; cursorId: string } | null>(null)
  const tracks = ref<Map<string, EvidenceTrackRegistration>>(new Map())

  const trackList = computed(() =>
    [...tracks.value.values()].map(track => ({
      trackId: track.trackId,
      panelId: track.panelId,
      trackLabel: track.trackLabel,
      sourceKey: track.sourceKey,
      pinCount: pins.value.filter(pin => pin.trackId === track.trackId).length,
    })),
  )

  const scopedPins = computed(() => {
    if (scopeMode.value === 'all') return [...pins.value].sort(comparePinsByCursor)
    if (scopeMode.value === 'current') {
      return pins.value.filter(pin => pin.trackId === currentTrackId.value).sort(comparePinsByCursor)
    }
    return pins.value.filter(pin => selectedTrackIds.value.has(pin.trackId)).sort(comparePinsByCursor)
  })

  function setDrawerOpen(open: boolean) {
    drawerOpen.value = open
    logEvidenceDiagnostic('info', open ? 'drawer.open' : 'drawer.close', { pinCount: pins.value.length })
  }

  function registerTrack(track: EvidenceTrackRegistration) {
    tracks.value.set(track.trackId, track)
    tracks.value = new Map(tracks.value)
    logEvidenceDiagnostic('debug', 'track.register', { trackId: track.trackId, panelId: track.panelId })
  }

  function unregisterTrack(trackId: string) {
    tracks.value.delete(trackId)
    tracks.value = new Map(tracks.value)
    selectedTrackIds.value.delete(trackId)
    selectedTrackIds.value = new Set(selectedTrackIds.value)
    if (currentTrackId.value === trackId) currentTrackId.value = null
    logEvidenceDiagnostic('debug', 'track.unregister', { trackId })
  }

  function nextPinSequence(): number {
    return pins.value.reduce((max, pin) => Math.max(max, pin.sequence), 0) + 1
  }

  function addPin(input: AddEvidencePinInput): EvidencePin {
    const existing = pins.value.find(pin => `${pin.trackId}:${pin.sourceKey}:${pin.logId}` === pinIdentity(input))
    if (existing) return existing
    const sequence = nextPinSequence()
    const pin: EvidencePin = {
      id: makePinId(),
      panelId: input.panelId,
      trackId: input.trackId,
      trackLabel: input.trackLabel,
      sourceKey: input.sourceKey,
      sequence,
      label: `P${sequence}`,
      color: EVIDENCE_PIN_COLORS[(sequence - 1) % EVIDENCE_PIN_COLORS.length],
      logId: String(input.log.id ?? ''),
      log: snapshotLog(input.log),
      note: '',
      createdAt: new Date().toISOString(),
    }
    pins.value.push(pin)
    logEvidenceDiagnostic('info', 'pin.add', {
      panelId: pin.panelId,
      trackId: pin.trackId,
      pinId: pin.id,
      pinLabel: pin.label,
      deploymentId: pin.log.deployment_id,
      cursorTime: pin.log.timestamp,
      cursorId: pin.log.id,
    })
    return pin
  }

  function removePin(pinId: string): EvidencePin | null {
    const index = pins.value.findIndex(pin => pin.id === pinId)
    if (index < 0) return null
    const [pin] = pins.value.splice(index, 1)
    logEvidenceDiagnostic('info', 'pin.remove', { panelId: pin.panelId, trackId: pin.trackId, pinId: pin.id, pinLabel: pin.label })
    return pin
  }

  function togglePin(input: AddEvidencePinInput): TogglePinResult {
    const existing = pins.value.find(pin => `${pin.trackId}:${pin.sourceKey}:${pin.logId}` === pinIdentity(input))
    if (existing) {
      removePin(existing.id)
      return { action: 'removed', pin: existing }
    }
    return { action: 'added', pin: addPin(input) }
  }

  function updateNote(pinId: string, note: string) {
    const pin = pins.value.find(item => item.id === pinId)
    if (!pin) return
    pin.note = note
    logEvidenceDiagnostic('info', 'pin.note.update', { panelId: pin.panelId, trackId: pin.trackId, pinId: pin.id, pinLabel: pin.label })
  }

  function pinFor(trackId: string, sourceKey: string, logId: string): EvidencePin | null {
    return pins.value.find(pin => pin.trackId === trackId && pin.sourceKey === sourceKey && pin.logId === String(logId)) ?? null
  }

  function setEvidenceScope(mode: EvidenceScopeMode, trackId: string | null = currentTrackId.value) {
    scopeMode.value = mode
    currentTrackId.value = trackId
    logEvidenceDiagnostic('info', 'scope.change', { trackId: trackId ?? undefined, mode })
  }

  function setSelectedTrackIds(trackIds: string[]) {
    selectedTrackIds.value = new Set(trackIds)
    logEvidenceDiagnostic('debug', 'scope.selected_tracks', { count: trackIds.length })
  }

  function logsByTrackForScope(): Record<string, LogEntry[]> {
    const out: Record<string, LogEntry[]> = {}
    for (const pin of scopedPins.value) {
      if (out[pin.trackId]) continue
      out[pin.trackId] = tracks.value.get(pin.trackId)?.getLogs() ?? []
    }
    return out
  }

  function exportModel() {
    return buildEvidenceExportModel({
      pins: scopedPins.value,
      logsByTrack: logsByTrackForScope(),
      skippedSegmentKeys: skippedSegmentKeys.value,
    })
  }

  function formatPinnedLinesMarkdownForScope() {
    return formatPinnedLinesMarkdown(scopedPins.value)
  }

  function formatEvidencePackageMarkdown() {
    return formatEvidenceMarkdown(exportModel())
  }

  function toggleSegmentSkipped(key: string) {
    const next = new Set(skippedSegmentKeys.value)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    skippedSegmentKeys.value = next
    logEvidenceDiagnostic('info', 'segment.toggle', { segmentKey: key, skipped: next.has(key) })
  }

  function setTimeSyncEnabled(enabled: boolean) {
    timeSyncEnabled.value = enabled
    logEvidenceDiagnostic('info', enabled ? 'time_sync.enable' : 'time_sync.disable')
  }

  function alignOtherTracksToLog(originTrackId: string, log: LogEntry) {
    activeTimeAnchor.value = { trackId: originTrackId, cursorTime: log.timestamp, cursorId: String(log.id ?? '') }
    logEvidenceDiagnostic('info', 'time_sync.anchor', {
      trackId: originTrackId,
      deploymentId: log.deployment_id,
      cursorTime: log.timestamp,
      cursorId: log.id,
    })
    for (const track of tracks.value.values()) {
      if (track.trackId === originTrackId) continue
      void track.alignToTime(log.timestamp, String(log.id ?? ''))
    }
  }

  async function jumpToPin(pinId: string): Promise<boolean> {
    const pin = pins.value.find(item => item.id === pinId)
    if (!pin) return false
    const track = tracks.value.get(pin.trackId)
    if (!track) return false
    const ok = await track.jumpToLog(pin.logId)
    logEvidenceDiagnostic(ok ? 'info' : 'warn', 'pin.jump', { trackId: pin.trackId, pinId: pin.id, pinLabel: pin.label, cursorId: pin.logId })
    return ok
  }

  function sameTimeCandidatesForPin(pinId: string): SameTimePinCandidate[] {
    const pin = pins.value.find(item => item.id === pinId)
    if (!pin) return []
    const candidates: SameTimePinCandidate[] = []
    for (const track of tracks.value.values()) {
      if (track.trackId === pin.trackId) continue
      const logs = track.getLogs()
      const index = nearestLogIndexByCursorTime(logs, pin.log.timestamp, pin.log.id)
      if (index < 0) continue
      const log = logs[index]
      const distanceMs = Math.abs(ms(log.timestamp) - ms(pin.log.timestamp))
      if (distanceMs > SAME_TIME_CANDIDATE_WINDOW_MS) continue
      // 候选列表只展示尚未在目标轨道打钉的邻近日志，避免一键重复制造证据点。
      if (pinFor(track.trackId, track.sourceKey, log.id)) continue
      candidates.push({ trackId: track.trackId, trackLabel: track.trackLabel, sourceKey: track.sourceKey, log, distanceMs })
    }
    return candidates.sort((a, b) => a.distanceMs - b.distanceMs)
  }

  function clearAll() {
    const count = pins.value.length
    pins.value = []
    skippedSegmentKeys.value = new Set()
    logEvidenceDiagnostic('info', 'pins.clear_all', { count })
  }

  return {
    pins,
    drawerOpen,
    scopeMode,
    currentTrackId,
    selectedTrackIds,
    skippedSegmentKeys,
    timeSyncEnabled,
    activeTimeAnchor,
    trackList,
    scopedPins,
    setDrawerOpen,
    registerTrack,
    unregisterTrack,
    addPin,
    removePin,
    togglePin,
    updateNote,
    pinFor,
    setEvidenceScope,
    setSelectedTrackIds,
    exportModel,
    formatPinnedLinesMarkdown: formatPinnedLinesMarkdownForScope,
    formatEvidencePackageMarkdown,
    toggleSegmentSkipped,
    setTimeSyncEnabled,
    alignOtherTracksToLog,
    jumpToPin,
    sameTimeCandidatesForPin,
    clearAll,
  }
})
