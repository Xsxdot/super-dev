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
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useGettingStartedStore } from '@/stores/gettingStarted'
import GettingStartedPanel from './GettingStartedPanel.vue'

const { t } = useAppI18n()
const gs = useGettingStartedStore()
const open = ref(false)
const entryWrap = ref<HTMLElement | null>(null)
const popoverPosition = ref({ left: 16, bottom: 16 })

const POPOVER_MAX_WIDTH = 520
const VIEWPORT_MARGIN = 16
const POPOVER_GAP = 8

function updatePopoverPosition() {
  const rect = entryWrap.value?.getBoundingClientRect()
  if (!rect) return

  // 弹层脱离侧栏后仍优先贴着侧栏右侧；窄窗口时向左收，保证完整落在视口内。
  const availableWidth = Math.max(0, window.innerWidth - VIEWPORT_MARGIN * 2)
  const panelWidth = Math.min(POPOVER_MAX_WIDTH, availableWidth)
  const preferredLeft = rect.right + POPOVER_GAP
  const maxLeft = window.innerWidth - panelWidth - VIEWPORT_MARGIN
  const left = Math.max(VIEWPORT_MARGIN, Math.min(preferredLeft, maxLeft))
  const bottom = Math.max(VIEWPORT_MARGIN, window.innerHeight - rect.top + POPOVER_GAP)

  popoverPosition.value = { left, bottom }
}

function stopTrackingPosition() {
  window.removeEventListener('resize', updatePopoverPosition)
}

async function toggle() {
  open.value = !open.value
  if (!open.value) {
    stopTrackingPosition()
    return
  }
  await nextTick()
  updatePopoverPosition()
  window.addEventListener('resize', updatePopoverPosition)
}

onBeforeUnmount(stopTrackingPosition)
</script>

<template>
  <div v-if="gs.visible" ref="entryWrap" class="gs-entry-wrap">
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
  </div>
  <Teleport to="body">
    <div
      v-if="gs.visible && open"
      class="gs-popover"
      data-test="getting-started-popover"
      :style="{ left: `${popoverPosition.left}px`, bottom: `${popoverPosition.bottom}px` }"
    >
      <GettingStartedPanel />
    </div>
  </Teleport>
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
  position: fixed;
  z-index: 70;
  max-height: calc(100vh - 32px);
}

.gs-popover :deep(.gs-panel) {
  max-height: calc(100vh - 32px);
  overflow-y: auto;
}
</style>
