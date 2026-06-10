<!--
起步旅程侧边栏入口

职责：
  - 在侧边栏底部展示「起步 N/5」入口与进度条
  - 点击切换起步旅程浮层清单
  - gettingStartedStore.visible 为 false 时整体不渲染

边界：
  - 不判定完成状态，只读取 gettingStartedStore
  - 浮层内容委托给 GettingStartedPanel
-->
<script setup lang="ts">
import { ref } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useGettingStartedStore } from '@/stores/gettingStarted'
import GettingStartedPanel from './GettingStartedPanel.vue'

const { t } = useAppI18n()
const gs = useGettingStartedStore()
const open = ref(false)

function toggle() {
  open.value = !open.value
}
</script>

<template>
  <div v-if="gs.visible" class="gs-entry-wrap">
    <button
      type="button"
      class="gs-entry"
      data-test="getting-started-entry"
      :aria-expanded="open"
      @click="toggle"
    >
      <span class="gs-entry-icon" aria-hidden="true"></span>
      <span class="gs-entry-main">{{ t('gettingStarted.progress', { done: gs.completedCount, total: gs.totalSteps }) }}</span>
      <span class="gs-entry-hint">{{ t('gettingStarted.entryHint') }}</span>
    </button>
    <div class="gs-progress-track" aria-hidden="true">
      <div class="gs-progress-fill" :style="{ width: `${(gs.completedCount / gs.totalSteps) * 100}%` }"></div>
    </div>
    <div v-if="open" class="gs-popover" data-test="getting-started-popover">
      <GettingStartedPanel />
    </div>
  </div>
</template>

<style scoped>
.gs-entry-wrap {
  position: relative;
}

.gs-entry {
  display: grid;
  width: 100%;
  min-height: 46px;
  grid-template-columns: 20px minmax(0, auto) minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border: 0;
  border-left: 2px solid var(--accent);
  border-radius: 6px;
  background: rgba(31, 111, 235, 0.13);
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
  transition: background 0.12s, color 0.12s;
}

.gs-entry:hover,
.gs-entry[aria-expanded='true'] {
  background: rgba(31, 111, 235, 0.19);
  color: var(--text-primary);
}

.gs-entry-icon {
  display: inline-flex;
  width: 16px;
  height: 16px;
  align-items: center;
  justify-content: center;
  justify-self: center;
  border: 2px solid rgba(88, 166, 255, 0.68);
  border-radius: 50%;
  color: var(--accent);
}

.gs-entry-icon::after {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  content: '';
}

.gs-entry-main {
  min-width: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
  white-space: nowrap;
}

.gs-entry-hint {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gs-progress-track {
  height: 3px;
  margin: 4px 9px 3px 32px;
  overflow: hidden;
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.18);
}

.gs-progress-fill {
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, var(--accent), var(--success));
  transition: width 0.18s ease;
}

.gs-popover {
  position: absolute;
  bottom: calc(100% + 8px);
  left: 246px;
  z-index: 70;
}
</style>
