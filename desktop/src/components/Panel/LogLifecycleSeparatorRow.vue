<!--
生命周期日志分隔行

职责：
  - 在当前前端会话中标记 deployment 启动、停止、重启操作

边界：
  - 不代表真实 LogEntry
  - 不持久化，不参与复制、导出或过滤
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { LogLifecycleMarker } from '@/stores/logLifecycle'

const props = defineProps<{
  marker: LogLifecycleMarker
}>()

const label = computed(() => {
  if (props.marker.kind === 'start') return '启动'
  if (props.marker.kind === 'stop') return '停止'
  return '重启'
})

const time = computed(() => {
  const d = new Date(props.marker.createdAt)
  return d.toLocaleTimeString('en-US', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
})
</script>

<template>
  <div class="lifecycle-separator-row" :class="marker.kind">
    <span class="line" />
    <span class="label">{{ label }} · {{ time }}</span>
    <span class="line" />
  </div>
</template>

<style scoped>
.lifecycle-separator-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  color: var(--text-tertiary);
  font-size: 10px;
}
.line {
  height: 1px;
  flex: 1;
  background: var(--border-secondary);
}
.label {
  white-space: nowrap;
}
.start .label { color: #3fb950; }
.stop .label { color: #f85149; }
.restart .label { color: #d29922; }
</style>
