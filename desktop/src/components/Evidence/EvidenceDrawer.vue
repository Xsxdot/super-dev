<script setup lang="ts">
// EvidenceDrawer 是日志证据包的悬浮工作台。
//
// 职责：
//   - 汇总当前 scope 下的 pins、segments 和 Markdown preview
//   - 提供复制 Markdown、导出 .md 和清空证据操作
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
import EvidenceTrackSelector from './EvidenceTrackSelector.vue'
import EvidenceTimelineList from './EvidenceTimelineList.vue'
import EvidenceSegmentList from './EvidenceSegmentList.vue'

type EvidenceTab = 'timeline' | 'pinned' | 'segments' | 'preview'
type CopyState = 'idle' | 'success' | 'error'

const store = useLogEvidenceStore()
const { t } = useI18n()
const activeTab = ref<EvidenceTab>('timeline')
const copyState = ref<CopyState>('idle')
let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null

const markdown = computed(() =>
  activeTab.value === 'pinned'
    ? store.formatPinnedLinesMarkdown()
    : store.formatEvidencePackageMarkdown(),
)

const copyButtonLabel = computed(() => {
  if (copyState.value === 'success') return t('common.copied')
  if (copyState.value === 'error') return t('panel.evidence.copyFailedShort')
  return t('panel.evidence.copyMarkdown')
})

onBeforeUnmount(() => {
  clearCopyFeedbackTimer()
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

async function copyMarkdown() {
  clearCopyFeedbackTimer()
  try {
    await navigator.clipboard.writeText(markdown.value)
    copyState.value = 'success'
    logEvidenceDiagnostic('info', 'evidence.copy.success', {
      pinCount: store.scopedPins.length,
      scopeMode: store.scopeMode,
      outputMode: activeTab.value,
    })
  } catch (err) {
    copyState.value = 'error'
    logEvidenceDiagnostic('error', 'evidence.copy.failure', {
      pinCount: store.scopedPins.length,
      scopeMode: store.scopeMode,
      outputMode: activeTab.value,
      error: err instanceof Error ? err.message : String(err),
    })
  } finally {
    scheduleCopyFeedbackReset()
  }
}

async function exportMarkdown() {
  logEvidenceDiagnostic('info', 'evidence.export.start', {
    pinCount: store.scopedPins.length,
    scopeMode: store.scopeMode,
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
      pinCount: store.scopedPins.length,
      scopeMode: store.scopeMode,
      outputMode: activeTab.value,
    })
  } catch (err) {
    logEvidenceDiagnostic('error', 'evidence.export.failure', {
      pinCount: store.scopedPins.length,
      scopeMode: store.scopeMode,
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
  <aside class="evidence-drawer" data-test="evidence-drawer">
    <header class="drawer-header">
      <div class="drawer-title">
        <span>{{ t('panel.evidence.title') }}</span>
        <span class="count" data-test="evidence-count">{{ t('panel.evidence.pinCount', { count: store.scopedPins.length }) }}</span>
      </div>
      <EvidenceTrackSelector />
      <div class="drawer-actions">
        <button
          type="button"
          class="action-btn copy-btn"
          :class="{ 'feedback-success': copyState === 'success', 'feedback-error': copyState === 'error' }"
          data-test="copy-evidence"
          @click="copyMarkdown"
        >
          {{ copyButtonLabel }}
        </button>
        <button type="button" class="action-btn" data-test="export-evidence" @click="exportMarkdown">{{ t('panel.evidence.exportMarkdown') }}</button>
        <button type="button" class="action-btn clear-btn" data-test="clear-evidence" @click="store.clearAll">{{ t('panel.evidence.clear') }}</button>
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
      <button type="button" :class="{ active: activeTab === 'pinned' }" data-test="tab-pinned" @click="activeTab = 'pinned'">{{ t('panel.evidence.pinned') }}</button>
      <button type="button" :class="{ active: activeTab === 'segments' }" data-test="tab-segments" @click="activeTab = 'segments'">{{ t('panel.evidence.segments') }}</button>
      <button type="button" :class="{ active: activeTab === 'preview' }" data-test="tab-preview" @click="activeTab = 'preview'">{{ t('panel.evidence.preview') }}</button>
    </nav>

    <section class="drawer-body">
      <EvidenceTimelineList v-if="activeTab === 'timeline'" />
      <pre v-else-if="activeTab === 'pinned'" class="preview" data-test="pinned-preview">{{ store.formatPinnedLinesMarkdown() }}</pre>
      <EvidenceSegmentList v-else-if="activeTab === 'segments'" />
      <pre v-else class="preview" data-test="evidence-preview">{{ markdown }}</pre>
    </section>
  </aside>
</template>

<style scoped>
.evidence-drawer {
  position: fixed;
  left: 16px;
  right: 16px;
  bottom: 82px;
  z-index: 900;
  min-height: 260px;
  max-height: 38vh;
  display: flex;
  flex-direction: column;
  border: 1px solid rgba(88, 166, 255, 0.34);
  border-radius: 8px;
  background: rgba(13, 24, 34, 0.98);
  box-shadow: 0 18px 42px rgba(0, 0, 0, 0.44);
  color: var(--text-secondary);
  overflow: hidden;
}

.drawer-header {
  display: grid;
  grid-template-columns: minmax(130px, auto) minmax(0, 1fr) auto 28px;
  grid-template-areas: 'title selector actions close';
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
  min-height: 44px;
  padding: 7px 10px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.16);
}

.drawer-title {
  grid-area: title;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 750;
  white-space: nowrap;
}

.count {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 600;
}

.drawer-actions {
  grid-area: actions;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

:deep(.track-selector) {
  grid-area: selector;
  min-width: 0;
}

.action-btn,
.icon-btn,
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

.action-btn {
  padding: 0 10px;
}

.copy-btn {
  min-width: 98px;
}

.clear-btn {
  min-width: 42px;
}

.icon-btn {
  padding: 0 8px;
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

.action-btn:hover,
.icon-btn:hover,
.drawer-close-btn:hover,
.drawer-tabs button:hover {
  color: var(--text-primary);
  background: rgba(255, 255, 255, 0.06);
}

.action-btn.feedback-success {
  border-color: rgba(63, 185, 80, 0.42);
  background: rgba(63, 185, 80, 0.16);
  color: #7ce38b;
}

.action-btn.feedback-error {
  border-color: rgba(248, 81, 73, 0.42);
  background: rgba(248, 81, 73, 0.14);
  color: #ff7b72;
}

.drawer-tabs {
  display: flex;
  flex-shrink: 0;
  gap: 6px;
  padding: 7px 10px 0;
}

.drawer-tabs button {
  padding: 0 10px;
  border-bottom-left-radius: 0;
  border-bottom-right-radius: 0;
}

.drawer-tabs button.active {
  border-color: rgba(88, 166, 255, 0.34);
  background: rgba(88, 166, 255, 0.12);
  color: #58a6ff;
}

.drawer-body {
  min-height: 0;
  overflow: auto;
  padding: 8px 10px 10px;
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

@media (max-height: 420px) {
  .evidence-drawer {
    bottom: 12px;
    min-height: min(260px, calc(100vh - 28px));
    max-height: calc(100vh - 28px);
  }
}

@media (max-width: 640px) {
  .evidence-drawer {
    left: 12px;
    right: 12px;
    max-height: 48vh;
  }

  .drawer-header {
    grid-template-columns: 1fr 28px;
    grid-template-areas:
      'title close'
      'selector selector'
      'actions actions';
    align-items: start;
    gap: 7px;
    min-height: 0;
  }

  :deep(.track-selector),
  .drawer-title,
  .drawer-actions {
    width: 100%;
  }

  .drawer-actions {
    justify-content: flex-start;
    overflow-x: auto;
    padding-bottom: 2px;
  }

  .action-btn,
  .icon-btn {
    flex: 0 0 auto;
  }

  .drawer-tabs {
    overflow-x: auto;
  }

  .preview {
    min-height: 150px;
  }
}
</style>
