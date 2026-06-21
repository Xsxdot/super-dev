<script setup lang="ts">
// EvidenceTrackSelector 管理证据抽屉的分栏范围选择。
//
// 职责：
//   - 切换 current / selected / all 范围
//   - 展示每个 track 的 pin 数并维护 selected tracks
//
// 边界：
//   - 不格式化导出内容
//   - 不执行跳转或复制
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useLogEvidenceStore, type EvidenceScopeMode } from '@/stores/logEvidence'

const store = useLogEvidenceStore()
const { t } = useI18n()

const selectedIds = computed(() => store.selectedTrackIds)

function setScope(mode: EvidenceScopeMode) {
  store.setEvidenceScope(mode, store.currentTrackId)
}

function toggleTrack(trackId: string, checked: boolean) {
  const next = new Set(store.selectedTrackIds)
  if (checked) next.add(trackId)
  else next.delete(trackId)
  store.setSelectedTrackIds([...next])
}
</script>

<template>
  <div class="track-selector" data-test="evidence-track-selector">
    <div class="scope-tabs">
      <button
        type="button"
        data-test="scope-current"
        :class="{ active: store.scopeMode === 'current' }"
        @click="setScope('current')"
      >
        {{ t('panel.evidence.scopeCurrent') }}
      </button>
      <button
        type="button"
        data-test="scope-selected"
        :class="{ active: store.scopeMode === 'selected' }"
        @click="setScope('selected')"
      >
        {{ t('panel.evidence.scopeSelected') }}
      </button>
      <button
        type="button"
        data-test="scope-all"
        :class="{ active: store.scopeMode === 'all' }"
        @click="setScope('all')"
      >
        {{ t('panel.evidence.scopeAll') }}
      </button>
    </div>
    <div v-if="store.scopeMode === 'selected'" class="track-list">
      <label
        v-for="track in store.trackList"
        :key="track.trackId"
        class="track-row"
      >
        <input
          type="checkbox"
          :data-test="`track-${track.trackId}`"
          :checked="selectedIds.has(track.trackId)"
          @change="toggleTrack(track.trackId, ($event.target as HTMLInputElement).checked)"
        />
        <span class="track-name">{{ track.trackLabel }}</span>
        <span class="track-count">{{ track.pinCount }}</span>
      </label>
    </div>
  </div>
</template>

<style scoped>
.track-selector {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.scope-tabs {
  display: inline-flex;
  overflow: hidden;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 5px;
}

.scope-tabs button {
  height: 26px;
  padding: 0 9px;
  border: 0;
  border-right: 1px solid rgba(139, 148, 158, 0.18);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
}

.scope-tabs button:last-child {
  border-right: 0;
}

.scope-tabs button.active {
  background: rgba(88, 166, 255, 0.14);
  color: #58a6ff;
}

.track-list {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  overflow-x: auto;
}

.track-row {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid rgba(139, 148, 158, 0.18);
  border-radius: 5px;
  color: var(--text-secondary);
  white-space: nowrap;
}

.track-row input {
  accent-color: #58a6ff;
}

.track-name {
  color: var(--text-primary);
  font-size: 11px;
}

.track-count {
  color: var(--text-tertiary);
  font-size: 10px;
}
</style>
