<script setup lang="ts">
// EvidenceSegmentList 展示同轨道证据区间的 Keep/Skip 控制。
//
// 职责：
//   - 按 track 展示相邻 pin 形成的区间
//   - 维护用户跳过某段区间的显式选择
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

function toggleSegment(key: string) {
  store.toggleSegmentSkipped(key)
}
</script>

<template>
  <div class="segment-list" data-test="segment-list">
    <div
      v-for="track in model.tracks"
      :key="track.trackId"
      class="segment-track"
    >
      <div class="segment-track-head">{{ track.trackLabel }}</div>
      <!-- 跨 track pins 只属于 Timeline；这里故意只展示同 track 相邻区间。 -->
      <div v-if="track.segments.length === 0" class="segment-empty">{{ t('panel.evidence.noSameTrackIntervals') }}</div>
      <div
        v-for="segment in track.segments"
        :key="segment.key"
        class="segment-row"
      >
        <span class="segment-name">{{ segment.from.label }} -> {{ segment.to.label }}</span>
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
  gap: 8px;
}

.segment-track {
  border: 1px solid rgba(139, 148, 158, 0.16);
  border-radius: 6px;
  overflow: hidden;
}

.segment-track-head {
  min-height: 28px;
  display: flex;
  align-items: center;
  padding: 0 9px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.12);
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 750;
}

.segment-empty,
.segment-row {
  min-height: 30px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 80px 64px;
  align-items: center;
  gap: 8px;
  padding: 0 9px;
  color: var(--text-secondary);
  font-size: 11px;
}

.segment-empty {
  display: flex;
  color: var(--text-tertiary);
}

.segment-name {
  color: var(--text-primary);
  font-weight: 650;
}

.segment-count {
  color: var(--text-tertiary);
  text-align: right;
}

.segment-toggle {
  height: 24px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
}
</style>
