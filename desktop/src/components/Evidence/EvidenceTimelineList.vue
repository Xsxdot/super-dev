<script setup lang="ts">
// EvidenceTimelineList 展示跨分栏的全局证据时间线。
//
// 职责：
//   - 按 cursor 顺序展示 scoped pins
//   - 触发跳转到对应 LogPanel 行
//   - 展示并显式添加同时间候选 pin
//
// 边界：
//   - 不生成导出 Markdown
//   - 不直接滚动日志面板
import { useLogEvidenceStore, type EvidencePin, type SameTimePinCandidate } from '@/stores/logEvidence'
import { logEvidenceDiagnostic } from '@/lib/logEvidenceDiagnostics'
import { useI18n } from 'vue-i18n'

const store = useLogEvidenceStore()
const { t } = useI18n()

function pinCandidate(candidate: SameTimePinCandidate) {
  const pin = store.addPin({
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

function summary(pin: EvidencePin): string {
  return pin.log.message.length > 120 ? `${pin.log.message.slice(0, 117)}...` : pin.log.message
}
</script>

<template>
  <div class="timeline-list" data-test="evidence-timeline-list">
    <div v-if="store.scopedPins.length === 0" class="empty">{{ t('panel.evidence.noPins') }}</div>
    <div
      v-for="pin in store.scopedPins"
      :key="pin.id"
      class="timeline-block"
    >
      <button
        type="button"
        class="timeline-item"
        data-test="evidence-timeline-item"
        @click="store.jumpToPin(pin.id)"
      >
        <span class="pin-badge" :style="{ color: pin.color, borderColor: pin.color }">{{ pin.label }}</span>
        <span class="track">{{ pin.trackLabel }}</span>
        <span class="time">{{ pin.log.timestamp }}</span>
        <span class="message">{{ summary(pin) }}</span>
        <span v-if="pin.note.trim()" class="note-dot" :title="t('panel.evidence.hasNote')" />
        <span class="cursor">{{ pin.log.id }}</span>
      </button>
      <div
        v-if="store.sameTimeCandidatesForPin(pin.id).length > 0"
        class="candidate-row"
        :title="t('panel.evidence.sameTimeCandidates')"
      >
        <button
          v-for="candidate in store.sameTimeCandidatesForPin(pin.id)"
          :key="`${pin.id}:${candidate.trackId}:${candidate.log.id}`"
          type="button"
          class="candidate-btn"
          data-test="same-time-candidate-pin"
          @click="pinCandidate(candidate)"
        >
          {{ candidate.trackLabel }} · {{ candidate.log.id }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.timeline-list {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-height: 0;
}

.empty {
  color: var(--text-tertiary);
  font-size: 12px;
  padding: 12px;
}

.timeline-block {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.timeline-item {
  display: grid;
  grid-template-columns: 42px minmax(82px, 130px) 190px minmax(0, 1fr) 12px 56px;
  align-items: center;
  gap: 8px;
  min-height: 30px;
  padding: 0 8px;
  border: 1px solid rgba(139, 148, 158, 0.16);
  border-radius: 5px;
  background: rgba(255, 255, 255, 0.025);
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
}

.timeline-item:hover {
  border-color: rgba(88, 166, 255, 0.32);
  background: rgba(88, 166, 255, 0.08);
}

.pin-badge {
  justify-self: start;
  min-width: 32px;
  padding: 2px 5px;
  border: 1px solid;
  border-radius: 4px;
  font-size: 10px;
  font-weight: 750;
  text-align: center;
}

.track,
.time,
.cursor {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.track {
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 650;
}

.time,
.cursor {
  color: var(--text-tertiary);
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  font-size: 10px;
}

.message {
  min-width: 0;
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.note-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #f5a524;
}

.candidate-row {
  display: flex;
  gap: 5px;
  padding-left: 50px;
}

.candidate-btn {
  height: 22px;
  padding: 0 7px;
  border: 1px solid rgba(54, 207, 201, 0.3);
  border-radius: 4px;
  background: rgba(54, 207, 201, 0.08);
  color: #36cfc9;
  cursor: pointer;
  font-size: 10px;
}
</style>
