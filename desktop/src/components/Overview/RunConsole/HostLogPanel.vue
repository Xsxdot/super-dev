<!--
HostLogPanel：运行控制台右侧日志面板。

职责：
  - 按选中的 step/host 渲染 run 日志
  - 将 run log 转为 logEngine 可处理的显示结构
  - 使用虚拟列表承载长日志，并在贴底时自动跟随最新输出

边界：
  - 不订阅 WebSocket
  - 不修改日志过滤规则
-->
<script setup lang="ts">
import { useVirtualizer } from '@tanstack/vue-virtual'
import { computed, nextTick, ref, watch } from 'vue'
import type { LogEntry, RunLogLine } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import { toDisplayEntry } from '@/lib/logEngine'

const props = defineProps<{
  logs: RunLogLine[]
  selectedStep: string
  selectedHost: string
  loading?: boolean
  running?: boolean
}>()

const { t } = useAppI18n()
const listEl = ref<HTMLElement | null>(null)
const pinnedToBottom = ref(true)
const fallbackRenderLimit = 200

function runLogToLogEntry(line: RunLogLine): LogEntry {
  return {
    id: line.id,
    deployment_id: line.step_name,
    run_id: line.run_id,
    timestamp: new Date(line.at).toISOString(),
    level: line.stream === 'stderr' ? 'ERROR' : 'INFO',
    message: line.line,
    stream: line.stream,
    source_id: line.host_id,
  }
}

const visible = computed(() => props.logs
  .filter(line => !props.selectedStep || line.step_name === props.selectedStep)
  .filter(line => !props.selectedHost || line.host_id === props.selectedHost)
  .map(line => ({
    raw: line,
    display: toDisplayEntry(runLogToLogEntry(line)),
  })))

const virtualizer = useVirtualizer(computed(() => ({
  count: visible.value.length,
  getScrollElement: () => listEl.value,
  estimateSize: () => 24,
  getItemKey: (index: number) => visible.value[index]?.raw.id ?? index,
  overscan: 20,
})))

const renderRows = computed(() => {
  const rows = virtualizer.value.getVirtualItems()
  if (rows.length > 0) return rows
  // jsdom 或零高度容器下虚拟器可能无法计算可见行；兜底渲染少量行，避免日志区域完全空白。
  return visible.value.slice(0, fallbackRenderLimit).map((entry, index) => ({
    key: entry.raw.id,
    index,
    start: index * 24,
  }))
})

const totalSize = computed(() =>
  Math.max(virtualizer.value.getTotalSize(), visible.value.length * 24),
)

function onScroll() {
  const el = listEl.value
  if (!el) return
  pinnedToBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 12
}

function scrollToBottom() {
  const el = listEl.value
  if (!el) return
  el.scrollTop = el.scrollHeight
  pinnedToBottom.value = true
}

watch(() => props.logs.length, async () => {
  if (!pinnedToBottom.value) return
  await nextTick()
  scrollToBottom()
})
</script>

<template>
  <section class="host-log-panel">
    <div class="log-breadcrumb">{{ selectedStep || t('runConsole.allSteps') }} · {{ selectedHost || t('runConsole.allHosts') }}</div>
    <div ref="listEl" class="run-log-list" @scroll="onScroll">
      <div v-if="loading" class="run-log-empty">{{ t('common.loading') }}</div>
      <div v-else-if="visible.length === 0" class="run-log-empty">
        {{ running ? t('runConsole.waitingOutput') : t('runConsole.noLogs') }}
      </div>
      <div v-else :style="{ height: totalSize + 'px', position: 'relative' }">
        <div
          v-for="vRow in renderRows"
          :key="String(vRow.key)"
          :data-index="vRow.index"
          :ref="(el) => { if (el) virtualizer.measureElement(el as Element) }"
          class="run-log-row"
          :class="visible[vRow.index]?.raw.stream"
          :style="{ position: 'absolute', top: vRow.start + 'px', width: '100%' }"
        >
          <span class="source-chip">[{{ visible[vRow.index]?.display.source_id || 'local' }}]</span>
          <span class="log-message">{{ visible[vRow.index]?.display.message }}</span>
        </div>
      </div>
      <button
        v-if="!pinnedToBottom && visible.length > 0"
        type="button"
        class="scroll-bottom"
        @click="scrollToBottom"
      >
        {{ t('runConsole.backToBottom') }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.host-log-panel {
  position: relative;
  min-width: 0;
  background: var(--bg-primary);
  color: var(--text-primary);
  overflow: hidden;
}
.log-breadcrumb {
  height: 36px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 12px;
}
.run-log-list {
  height: calc(100% - 36px);
  overflow: auto;
  padding: 8px 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
.run-log-empty {
  display: flex;
  height: 100%;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-family: ui-sans-serif, system-ui, sans-serif;
}
.run-log-row {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr);
  gap: 8px;
  min-height: 24px;
  padding: 3px 12px;
}
.run-log-row:hover {
  background: var(--bg-overlay);
}
.run-log-row.stderr .log-message {
  color: var(--status-failed);
}
.source-chip {
  overflow: hidden;
  color: var(--text-tertiary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.log-message {
  min-width: 0;
  white-space: pre-wrap;
  word-break: break-word;
}
.scroll-bottom {
  position: absolute;
  right: 16px;
  bottom: 16px;
  height: 28px;
  padding: 0 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  background: var(--bg-overlay);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
</style>
