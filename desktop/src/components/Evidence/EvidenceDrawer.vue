<script setup lang="ts">
// EvidenceDrawer 是日志证据包的悬浮工作台。
//
// 职责：
//   - 汇总当前 tab 下的 pins、segments 和 Markdown preview
//   - 提供复制 Markdown、导出 .md 和清空证据操作
//   - 维护左侧浮层的位置和尺寸
//
// 边界：
//   - 不直接操作 LogPanel DOM
//   - 不持久化 pins
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'
import { save } from '@tauri-apps/plugin-dialog'
import { writeTextFile } from '@tauri-apps/plugin-fs'
import { useLogEvidenceStore } from '@/stores/logEvidence'
import { logEvidenceDiagnostic } from '@/lib/logEvidenceDiagnostics'
import EvidenceTimelineList from './EvidenceTimelineList.vue'
import EvidenceSegmentList from './EvidenceSegmentList.vue'

type EvidenceTab = 'timeline' | 'segments' | 'preview'
type CopyState = 'idle' | 'success' | 'error'

const store = useLogEvidenceStore()
const { t } = useI18n()
const activeTab = ref<EvidenceTab>('timeline')
const copyState = ref<CopyState>('idle')
const drawerFrame = ref({ left: 74, top: 48, width: 348, height: 760 })
let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null
let dragState: { kind: 'move' | 'resize'; startX: number; startY: number; startFrame: typeof drawerFrame.value } | null = null

const markdown = computed(() => store.formatEvidencePackageMarkdown())

const drawerStyle = computed(() => ({
  left: `${drawerFrame.value.left}px`,
  top: `${drawerFrame.value.top}px`,
  width: `${drawerFrame.value.width}px`,
  height: `${Math.min(drawerFrame.value.height, window.innerHeight - drawerFrame.value.top - 16)}px`,
}))

const copyButtonLabel = computed(() => {
  if (copyState.value === 'success') return t('common.copied')
  if (copyState.value === 'error') return t('panel.evidence.copyFailedShort')
  return t('panel.evidence.copySelected')
})

onBeforeUnmount(() => {
  clearCopyFeedbackTimer()
  stopPointerInteraction()
})

function clearCopyFeedbackTimer() {
  if (!copyFeedbackTimer) return
  clearTimeout(copyFeedbackTimer)
  copyFeedbackTimer = null
}

function scheduleCopyFeedbackReset() {
  clearCopyFeedbackTimer()
  copyFeedbackTimer = setTimeout(() => {
    copyState.value = 'idle'
    copyFeedbackTimer = null
  }, 1600)
}

function clampFrame(frame: typeof drawerFrame.value) {
  const maxWidth = Math.max(320, window.innerWidth - 32)
  const maxHeight = Math.max(360, window.innerHeight - 32)
  return {
    left: Math.min(Math.max(8, frame.left), window.innerWidth - 120),
    top: Math.min(Math.max(8, frame.top), window.innerHeight - 120),
    width: Math.min(Math.max(300, frame.width), maxWidth),
    height: Math.min(Math.max(360, frame.height), maxHeight),
  }
}

function startPointerInteraction(kind: 'move' | 'resize', event: MouseEvent) {
  event.preventDefault()
  dragState = {
    kind,
    startX: event.clientX,
    startY: event.clientY,
    startFrame: { ...drawerFrame.value },
  }
  window.addEventListener('mousemove', onPointerInteraction)
  window.addEventListener('mouseup', stopPointerInteraction)
}

function onPointerInteraction(event: MouseEvent) {
  if (!dragState) return
  const dx = event.clientX - dragState.startX
  const dy = event.clientY - dragState.startY
  if (dragState.kind === 'move') {
    drawerFrame.value = clampFrame({
      ...dragState.startFrame,
      left: dragState.startFrame.left + dx,
      top: dragState.startFrame.top + dy,
    })
    return
  }
  drawerFrame.value = clampFrame({
    ...dragState.startFrame,
    width: dragState.startFrame.width + dx,
    height: dragState.startFrame.height + dy,
  })
}

function stopPointerInteraction() {
  dragState = null
  window.removeEventListener('mousemove', onPointerInteraction)
  window.removeEventListener('mouseup', stopPointerInteraction)
}

async function copyMarkdown() {
  clearCopyFeedbackTimer()
  try {
    await navigator.clipboard.writeText(markdown.value)
    copyState.value = 'success'
    logEvidenceDiagnostic('info', 'evidence.copy.success', {
      pinCount: store.activePins.length,
      outputMode: activeTab.value,
    })
  } catch (err) {
    copyState.value = 'error'
    logEvidenceDiagnostic('error', 'evidence.copy.failure', {
      pinCount: store.activePins.length,
      outputMode: activeTab.value,
      error: err instanceof Error ? err.message : String(err),
    })
  } finally {
    scheduleCopyFeedbackReset()
  }
}

async function exportMarkdown() {
  logEvidenceDiagnostic('info', 'evidence.export.start', {
    pinCount: store.activePins.length,
    outputMode: activeTab.value,
  })
  const selected = await save({
    defaultPath: `superdev-log-evidence-${Date.now()}.md`,
    title: t('panel.evidence.exportTitle'),
    filters: [{ name: 'Markdown', extensions: ['md'] }],
  })
  if (!selected) return
  try {
    await writeTextFile(selected, markdown.value)
    logEvidenceDiagnostic('info', 'evidence.export.success', {
      pinCount: store.activePins.length,
      outputMode: activeTab.value,
    })
  } catch (err) {
    logEvidenceDiagnostic('error', 'evidence.export.failure', {
      pinCount: store.activePins.length,
      outputMode: activeTab.value,
      error: err instanceof Error ? err.message : String(err),
    })
  }
}

function closeDrawer() {
  store.setDrawerOpen(false)
}
</script>

<template>
  <aside class="evidence-drawer left-floating" data-test="evidence-drawer" :style="drawerStyle">
    <header class="drawer-header">
      <button
        type="button"
        class="drag-handle"
        data-test="evidence-drag-handle"
        :title="t('panel.evidence.dragHandle')"
        @mousedown="startPointerInteraction('move', $event)"
      >
        <Icon icon="lucide:grip-vertical" aria-hidden="true" />
      </button>
      <div class="drawer-title">
        <span class="title-main">
          <Icon icon="lucide:pin" aria-hidden="true" />
          {{ t('panel.evidence.title') }}
        </span>
        <span class="count" data-test="evidence-count">{{ t('panel.evidence.pinCount', { count: store.activePins.length }) }}</span>
        <span class="tab-scope">{{ t('panel.evidence.currentTabOnly') }}</span>
      </div>
      <button
        type="button"
        class="drawer-close-btn"
        data-test="close-evidence"
        :title="t('panel.evidence.close')"
        @click="closeDrawer"
      >
        <Icon icon="lucide:x" aria-hidden="true" />
      </button>
    </header>

    <nav class="drawer-tabs">
      <button type="button" :class="{ active: activeTab === 'timeline' }" data-test="tab-timeline" @click="activeTab = 'timeline'">{{ t('panel.evidence.timeline') }}</button>
      <button type="button" :class="{ active: activeTab === 'segments' }" data-test="tab-segments" @click="activeTab = 'segments'">{{ t('panel.evidence.segments') }}</button>
      <button type="button" :class="{ active: activeTab === 'preview' }" data-test="tab-preview" @click="activeTab = 'preview'">{{ t('panel.evidence.preview') }}</button>
    </nav>

    <section class="drawer-body">
      <EvidenceTimelineList v-if="activeTab === 'timeline'" />
      <EvidenceSegmentList v-else-if="activeTab === 'segments'" />
      <pre v-else class="preview" data-test="evidence-preview">{{ markdown }}</pre>
    </section>
    <footer v-if="activeTab !== 'timeline'" class="drawer-footer" data-test="drawer-footer-actions">
      <button
        type="button"
        class="footer-action copy-btn"
        :class="{ 'feedback-success': copyState === 'success', 'feedback-error': copyState === 'error' }"
        data-test="copy-evidence"
        @click="copyMarkdown"
      >
        <Icon icon="lucide:copy" aria-hidden="true" />
        {{ copyButtonLabel }}
      </button>
      <button type="button" class="footer-action" data-test="export-evidence" @click="exportMarkdown">
        <Icon icon="lucide:download" aria-hidden="true" />
        {{ t('panel.evidence.exportSelected') }}
      </button>
      <button type="button" class="footer-action clear-btn" data-test="clear-evidence" @click="store.clearAll">
        <Icon icon="lucide:trash-2" aria-hidden="true" />
        {{ t('panel.evidence.clear') }}
      </button>
    </footer>
    <button
      type="button"
      class="resize-handle"
      data-test="evidence-resize-handle"
      :title="t('panel.evidence.resizeHandle')"
      @mousedown="startPointerInteraction('resize', $event)"
    />
  </aside>
</template>

<style scoped>
.evidence-drawer {
  position: fixed;
  z-index: 900;
  display: flex;
  flex-direction: column;
  border: 1px solid rgba(88, 166, 255, 0.42);
  border-radius: 9px;
  background: rgba(11, 20, 29, 0.96);
  box-shadow: 0 20px 54px rgba(0, 0, 0, 0.48), 0 0 0 1px rgba(88, 166, 255, 0.08) inset;
  backdrop-filter: blur(10px);
  color: var(--text-secondary);
  overflow: hidden;
}

.drawer-header {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr) 28px;
  grid-template-areas: 'drag title close';
  align-items: center;
  column-gap: 8px;
  flex-shrink: 0;
  min-height: 54px;
  padding: 8px 10px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.16);
}

.drag-handle {
  grid-area: drag;
  width: 24px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--text-tertiary);
  cursor: move;
}

.drag-handle svg {
  width: 14px;
  height: 14px;
}

.drawer-title {
  grid-area: title;
  min-width: 0;
  display: grid;
  grid-template-columns: minmax(0, auto) auto;
  grid-template-areas:
    'main count'
    'scope scope';
  align-items: center;
  justify-content: start;
  gap: 3px 8px;
  color: var(--text-primary);
}

.title-main {
  grid-area: main;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow: hidden;
  font-size: 13px;
  font-weight: 780;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.title-main svg {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
  color: #e6edf3;
}

.count {
  grid-area: count;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}

.tab-scope {
  grid-area: scope;
  width: fit-content;
  padding: 2px 0;
  border-radius: 999px;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 650;
}

.drawer-close-btn,
.drawer-tabs button {
  height: 28px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 5px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}

.drawer-close-btn {
  grid-area: close;
  width: 28px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  justify-self: end;
}

.drawer-close-btn svg {
  width: 14px;
  height: 14px;
}

.drawer-close-btn:hover,
.drawer-tabs button:hover {
  color: var(--text-primary);
  background: rgba(255, 255, 255, 0.06);
}

.drawer-tabs {
  display: flex;
  flex-shrink: 0;
  gap: 4px;
  padding: 8px 10px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.1);
}

.drawer-tabs button {
  flex: 1 1 0;
  padding: 0 10px;
  border-radius: 5px;
}

.drawer-tabs button.active {
  border-color: rgba(88, 166, 255, 0.34);
  background: rgba(88, 166, 255, 0.12);
  color: #58a6ff;
}

.drawer-body {
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 10px;
  scrollbar-width: thin;
}

.preview {
  min-height: 180px;
  margin: 0;
  padding: 9px;
  border: 1px solid rgba(139, 148, 158, 0.16);
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.18);
  color: var(--text-secondary);
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  font-size: 11px;
  line-height: 1.5;
  white-space: pre-wrap;
}

.drawer-footer {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 7px;
  flex-shrink: 0;
  padding: 9px 10px 12px;
  border-top: 1px solid rgba(139, 148, 158, 0.14);
  background: rgba(8, 16, 24, 0.74);
}

.footer-action {
  min-width: 0;
  height: 31px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 10px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.04);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  font-weight: 650;
  white-space: nowrap;
}

.footer-action svg {
  width: 13px;
  height: 13px;
}

.footer-action:hover {
  border-color: rgba(88, 166, 255, 0.32);
  color: var(--text-primary);
  background: rgba(88, 166, 255, 0.09);
}

.footer-action.feedback-success {
  border-color: rgba(63, 185, 80, 0.42);
  background: rgba(63, 185, 80, 0.16);
  color: #7ce38b;
}

.footer-action.feedback-error {
  border-color: rgba(248, 81, 73, 0.42);
  background: rgba(248, 81, 73, 0.14);
  color: #ff7b72;
}

.clear-btn {
  width: 74px;
}

.resize-handle {
  position: absolute;
  right: 0;
  bottom: 0;
  width: 18px;
  height: 18px;
  border: 0;
  background:
    linear-gradient(135deg, transparent 52%, rgba(88, 166, 255, 0.56) 53%, rgba(88, 166, 255, 0.56) 60%, transparent 61%),
    linear-gradient(135deg, transparent 68%, rgba(88, 166, 255, 0.36) 69%, rgba(88, 166, 255, 0.36) 76%, transparent 77%);
  cursor: nwse-resize;
}

@media (max-height: 420px) {
  .evidence-drawer {
    height: calc(100vh - 24px) !important;
  }
}

@media (max-width: 640px) {
  .evidence-drawer {
    left: 12px !important;
    width: calc(100vw - 24px) !important;
  }

  .drawer-header {
    grid-template-columns: 24px 1fr 28px;
    grid-template-areas: 'drag title close';
    align-items: start;
    gap: 7px;
    min-height: 0;
  }

  .drawer-title {
    width: 100%;
  }

  .drawer-tabs {
    overflow-x: auto;
  }

  .drawer-footer {
    grid-template-columns: 1fr 1fr;
  }

  .clear-btn {
    width: auto;
    grid-column: 1 / -1;
  }

  .preview {
    min-height: 150px;
  }
}
</style>
