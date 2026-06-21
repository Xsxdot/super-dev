<script setup lang="ts">
// EvidenceTimelineList 展示跨分栏的全局证据时间线。
//
// 职责：
//   - 按 cursor 顺序展示当前 tab 的 pins
//   - 触发跳转到对应 LogPanel 行
//   - 展示单 pin 操作菜单和选中 pin 的同时间候选
//
// 边界：
//   - 不生成导出 Markdown
//   - 不直接滚动日志面板
import { useLogEvidenceStore, type EvidencePin, type SameTimePinCandidate } from '@/stores/logEvidence'
import { formatLogWithCursor } from '@/lib/logEvidenceFormat'
import { logEvidenceDiagnostic } from '@/lib/logEvidenceDiagnostics'
import { Icon } from '@iconify/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const store = useLogEvidenceStore()
const { t } = useI18n()
const selectedPinId = ref<string | null>(null)
const openMenuPinId = ref<string | null>(null)

const selectedPin = computed(() => store.activePins.find(pin => pin.id === selectedPinId.value) ?? null)
const selectedCandidates = computed(() => selectedPin.value ? store.sameTimeCandidatesForPin(selectedPin.value.id) : [])

function selectPin(pin: EvidencePin) {
  selectedPinId.value = pin.id
  openMenuPinId.value = null
  logEvidenceDiagnostic('debug', 'timeline.pin.select', {
    trackId: pin.trackId,
    pinId: pin.id,
    pinLabel: pin.label,
    cursorId: pin.logId,
  })
  void store.jumpToPin(pin.id)
}

function pinCandidate(candidate: SameTimePinCandidate) {
  const pin = store.addPin({
    workspaceTabId: store.activeWorkspaceTabId,
    panelId: candidate.trackId,
    trackId: candidate.trackId,
    trackLabel: candidate.trackLabel,
    sourceKey: candidate.sourceKey,
    log: candidate.log,
  })
  logEvidenceDiagnostic('info', 'candidate.pin.add', {
    trackId: pin.trackId,
    pinId: pin.id,
    pinLabel: pin.label,
    deploymentId: pin.log.deployment_id,
    cursorTime: pin.log.timestamp,
    cursorId: pin.log.id,
  })
}

function togglePinMenu(pin: EvidencePin, event: MouseEvent) {
  event.stopPropagation()
  selectedPinId.value = pin.id
  openMenuPinId.value = openMenuPinId.value === pin.id ? null : pin.id
}

async function copyPinLog(pin: EvidencePin, withCursor: boolean) {
  const event = withCursor ? 'timeline.copy_with_cursor' : 'timeline.copy'
  try {
    await navigator.clipboard.writeText(withCursor ? formatLogWithCursor(pin.log) : pin.log.message)
    logEvidenceDiagnostic('info', `${event}.success`, {
      trackId: pin.trackId,
      pinId: pin.id,
      pinLabel: pin.label,
      deploymentId: pin.log.deployment_id,
      cursorTime: pin.log.timestamp,
      cursorId: pin.log.id,
    })
  } catch (err) {
    logEvidenceDiagnostic('error', `${event}.failure`, {
      trackId: pin.trackId,
      pinId: pin.id,
      pinLabel: pin.label,
      deploymentId: pin.log.deployment_id,
      cursorTime: pin.log.timestamp,
      cursorId: pin.log.id,
      error: err instanceof Error ? err.message : String(err),
    })
  } finally {
    openMenuPinId.value = null
  }
}

function removePin(pin: EvidencePin) {
  store.removePin(pin.id)
  if (selectedPinId.value === pin.id) selectedPinId.value = null
  openMenuPinId.value = null
}

function summary(pin: EvidencePin): string {
  return pin.log.message.length > 120 ? `${pin.log.message.slice(0, 117)}...` : pin.log.message
}

function candidateLabel(candidate: SameTimePinCandidate): string {
  const base = candidate.trackLabel.split(' · ')[0] || candidate.trackLabel
  return `${base} #${candidate.log.id}`
}
</script>

<template>
  <div class="timeline-list timeline-rail" data-test="evidence-timeline-list">
    <div v-if="store.activePins.length === 0" class="empty">{{ t('panel.evidence.noPins') }}</div>
    <div
      v-for="pin in store.activePins"
      :key="pin.id"
      class="timeline-block"
    >
      <div
        class="timeline-item"
        :class="{ selected: selectedPinId === pin.id }"
        data-test="evidence-timeline-item"
        role="button"
        tabindex="0"
        @click="selectPin(pin)"
        @keydown.enter.prevent="selectPin(pin)"
        @keydown.space.prevent="selectPin(pin)"
      >
        <span class="pin-badge" :style="{ color: pin.color, borderColor: pin.color }">{{ pin.label }}</span>
        <span class="pin-content">
          <span class="pin-head">
            <span class="time">{{ pin.log.timestamp }}</span>
          </span>
          <span class="message">{{ summary(pin) }}</span>
          <span class="source-chip">{{ pin.trackLabel }} #{{ pin.log.id }}</span>
        </span>
        <span v-if="pin.note.trim()" class="note-dot" :title="t('panel.evidence.hasNote')" />
        <span class="pin-menu-wrap">
          <button
            type="button"
            class="pin-more"
            data-test="timeline-pin-more"
            :aria-label="t('common.actions')"
            @click="togglePinMenu(pin, $event)"
          >
            <Icon icon="lucide:more-horizontal" aria-hidden="true" />
          </button>
          <span v-if="openMenuPinId === pin.id" class="pin-menu" data-test="timeline-pin-menu" @click.stop>
            <button type="button" data-test="timeline-copy-log" @click="copyPinLog(pin, false)">{{ t('panel.evidence.copyLog') }}</button>
            <button type="button" data-test="timeline-copy-log-with-cursor" @click="copyPinLog(pin, true)">{{ t('panel.evidence.copyWithCursor') }}</button>
            <button type="button" class="danger" data-test="timeline-remove-pin" @click="removePin(pin)">{{ t('panel.evidence.unpin') }}</button>
          </span>
        </span>
      </div>
    </div>

    <div
      v-if="selectedPin && store.trackList.length > 1"
      class="candidate-panel"
      data-test="same-time-candidate-panel"
    >
      <div class="candidate-panel-title">
        <span>{{ t('panel.evidence.pinOtherPanes') }}</span>
        <Icon icon="lucide:circle-help" aria-hidden="true" />
      </div>
      <div v-if="selectedCandidates.length > 0" class="candidate-grid">
        <div
          v-for="candidate in selectedCandidates"
          :key="`${selectedPin.id}:${candidate.trackId}:${candidate.log.id}`"
          class="candidate-card"
        >
          <span class="candidate-service">{{ candidate.trackLabel }}</span>
          <span class="candidate-cursor">#{{ candidate.log.id }}</span>
          <button
            type="button"
            class="candidate-btn"
            data-test="same-time-candidate-pin"
            :title="`${candidate.trackLabel} · ${candidate.log.timestamp} · #${candidate.log.id}`"
            @click="pinCandidate(candidate)"
          >
            + {{ t('panel.evidence.pin') }}
            <span>{{ candidateLabel(candidate) }}</span>
          </button>
        </div>
      </div>
      <div v-else class="candidate-empty">{{ t('panel.evidence.noNearbyLogLoaded') }}</div>
    </div>
  </div>
</template>

<style scoped>
.timeline-list {
  display: flex;
  flex-direction: column;
  gap: 0;
  min-height: 0;
  padding: 2px 0 4px;
}

.empty {
  color: var(--text-tertiary);
  font-size: 12px;
  padding: 12px;
}

.timeline-block {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 0 0 12px 0;
}

.timeline-block::before {
  content: '';
  position: absolute;
  left: 16px;
  top: 28px;
  bottom: -2px;
  width: 1px;
  background: rgba(139, 148, 158, 0.18);
}

.timeline-block:last-child::before {
  display: none;
}

.timeline-item {
  position: relative;
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr) 10px 24px;
  align-items: start;
  gap: 7px;
  min-height: 58px;
  width: 100%;
  padding: 5px 5px 6px 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
}

.timeline-item:hover {
  background: rgba(88, 166, 255, 0.08);
}

.timeline-item.selected {
  background: linear-gradient(90deg, rgba(88, 166, 255, 0.18), rgba(255, 255, 255, 0.035));
}

.pin-badge {
  justify-self: start;
  min-width: 30px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid;
  border-radius: 5px;
  background: rgba(255, 255, 255, 0.035);
  font-size: 10px;
  font-weight: 750;
  text-align: center;
}

.pin-content {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.pin-head {
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 7px;
}

.time,
.source-chip {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.time {
  color: var(--text-tertiary);
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  font-size: 9px;
}

.message {
  min-width: 0;
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-chip {
  width: fit-content;
  max-width: 100%;
  padding: 2px 6px;
  border: 1px solid rgba(139, 148, 158, 0.18);
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-tertiary);
  font-size: 10px;
  line-height: 1.2;
}

.note-dot {
  align-self: center;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #f5a524;
}

.pin-menu-wrap {
  position: relative;
  justify-self: end;
}

.pin-more {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-tertiary);
  cursor: pointer;
}

.pin-more:hover,
.pin-more:focus-visible {
  background: rgba(255, 255, 255, 0.08);
  color: var(--text-primary);
}

.pin-more svg {
  width: 15px;
  height: 15px;
}

.pin-menu {
  position: absolute;
  top: 26px;
  right: 0;
  z-index: 4;
  min-width: 150px;
  padding: 5px;
  border: 1px solid rgba(88, 166, 255, 0.28);
  border-radius: 6px;
  background: rgba(13, 24, 34, 0.98);
  box-shadow: 0 14px 30px rgba(0, 0, 0, 0.34);
}

.pin-menu button {
  width: 100%;
  height: 28px;
  display: flex;
  align-items: center;
  padding: 0 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  text-align: left;
  white-space: nowrap;
}

.pin-menu button:hover {
  background: rgba(88, 166, 255, 0.12);
  color: var(--text-primary);
}

.pin-menu .danger {
  color: #ff7b72;
}

.candidate-panel {
  position: sticky;
  bottom: -4px;
  margin-top: 2px;
  padding: 10px;
  border-top: 1px solid rgba(139, 148, 158, 0.12);
  background: rgba(15, 25, 35, 0.96);
  box-shadow: 0 -10px 24px rgba(0, 0, 0, 0.22);
}

.candidate-panel-title {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-bottom: 8px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 650;
}

.candidate-panel-title svg {
  width: 12px;
  height: 12px;
}

.candidate-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 7px;
}

.candidate-card {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 4px 7px;
  min-height: 48px;
  padding: 7px;
  border: 1px solid rgba(63, 185, 80, 0.24);
  border-radius: 6px;
  background: rgba(63, 185, 80, 0.055);
}

.candidate-service,
.candidate-cursor {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.candidate-service {
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 700;
}

.candidate-cursor {
  grid-column: 1;
  color: var(--text-tertiary);
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  font-size: 10px;
}

.candidate-btn {
  grid-row: 1 / span 2;
  grid-column: 2;
  height: 24px;
  padding: 0 8px;
  border: 1px solid rgba(63, 185, 80, 0.3);
  border-radius: 5px;
  background: rgba(46, 160, 67, 0.18);
  color: #7ce38b;
  cursor: pointer;
  font-size: 10px;
  font-weight: 750;
  white-space: nowrap;
}

.candidate-btn span {
  display: none;
}

.candidate-empty {
  color: var(--text-tertiary);
  font-size: 11px;
}
</style>
