<script setup lang="ts">
// EvidenceSegmentList 展示同轨道证据区间的 Keep/Skip 控制。
//
// 职责：
//   - 按 track 展示相邻 pin 形成的区间
//   - 维护用户跳过某段区间和导出选择的显式选择
//
// 边界：
//   - 不展示跨 track 区间
//   - 不执行复制或导出
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useLogEvidenceStore } from '@/stores/logEvidence'

const store = useLogEvidenceStore()
const { t } = useI18n()
const model = computed(() => store.exportModel())
const selectableSegmentKeys = computed(() =>
  model.value.tracks.flatMap(track => track.segments.filter(segment => !segment.skipped).map(segment => segment.key)),
)
const selectedCount = computed(() =>
  model.value.tracks.reduce((count, track) => count + track.segments.filter(segment => segment.selected).length, 0),
)

function toggleSegment(key: string) {
  store.toggleSegmentSkipped(key)
}

function toggleSelection(key: string) {
  store.toggleSegmentSelection(key)
}
</script>

<template>
  <div class="segment-list" data-test="segment-list">
    <div class="segment-toolbar">
      <button type="button" class="segment-tool" data-test="segments-select-all" @click="store.selectAllSegments(selectableSegmentKeys)">
        {{ t('panel.evidence.selectAll') }}
      </button>
      <button type="button" class="segment-tool" data-test="segments-deselect-all" @click="store.deselectAllSegments(selectableSegmentKeys)">
        {{ t('panel.evidence.deselectAll') }}
      </button>
      <span class="segment-selected">{{ t('panel.evidence.selectedSegmentCount', { selected: selectedCount, total: selectableSegmentKeys.length }) }}</span>
    </div>
    <div
      v-for="track in model.tracks"
      :key="track.trackId"
      class="segment-track"
    >
      <div class="segment-track-head">
        <span>{{ track.trackLabel }}</span>
        <span>{{ t('panel.evidence.trackPinCount', { count: track.pins.length }) }}</span>
      </div>
      <!-- 跨 track pins 只属于 Timeline；这里故意只展示同 track 相邻区间。 -->
      <div v-if="track.segments.length === 0" class="segment-empty">{{ t('panel.evidence.noSameTrackIntervals') }}</div>
      <div
        v-for="segment in track.segments"
        :key="segment.key"
        class="segment-row"
        :class="{ disabled: segment.skipped }"
      >
        <label class="segment-check">
          <input
            type="checkbox"
            data-test="segment-select"
            :checked="segment.selected"
            :disabled="segment.skipped"
            @change="toggleSelection(segment.key)"
          />
          <span class="segment-name">{{ segment.from.label }} -> {{ segment.to.label }}</span>
        </label>
        <span class="segment-count">{{ t('panel.evidence.logCount', { count: segment.logs.length }) }}</span>
        <button
          type="button"
          class="segment-toggle"
          data-test="segment-skip"
          @click="toggleSegment(segment.key)"
        >
          {{ segment.skipped ? t('panel.evidence.keep') : t('panel.evidence.skip') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.segment-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.segment-toolbar {
  min-height: 28px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 2px 2px;
  color: var(--text-tertiary);
  font-size: 11px;
}

.segment-tool {
  height: 24px;
  border: 0;
  background: transparent;
  color: #58a6ff;
  cursor: pointer;
  font-size: 11px;
}

.segment-selected {
  margin-left: auto;
  white-space: nowrap;
}

.segment-track {
  border: 1px solid rgba(139, 148, 158, 0.14);
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.018);
  overflow: hidden;
}

.segment-track-head {
  min-height: 30px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 9px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.1);
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 750;
}

.segment-track-head span:last-child {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 650;
}

.segment-empty,
.segment-row {
  min-height: 38px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 64px 54px;
  align-items: center;
  gap: 7px;
  padding: 0 9px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.08);
  color: var(--text-secondary);
  font-size: 11px;
}

.segment-row:last-child {
  border-bottom: 0;
}

.segment-row.disabled {
  opacity: 0.58;
}

.segment-empty {
  display: flex;
  color: var(--text-tertiary);
}

.segment-check {
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.segment-check input {
  accent-color: #58a6ff;
  width: 14px;
  height: 14px;
}

.segment-name {
  color: var(--text-primary);
  font-weight: 650;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.segment-count {
  color: var(--text-tertiary);
  text-align: right;
}

.segment-toggle {
  height: 24px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 5px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
}
</style>
