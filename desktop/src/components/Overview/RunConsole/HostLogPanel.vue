<!--
HostLogPanel：运行控制台右侧日志面板。

职责：
  - 按选中的 step/host 渲染 run 日志
  - 将 run log 转为 logEngine 可处理的显示结构
  - 显示每条日志的来源主机

边界：
  - 不订阅 WebSocket
  - 不修改日志过滤规则
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { LogEntry, RunLogLine } from '@/api/agent'
import { toDisplayEntry } from '@/lib/logEngine'

const props = defineProps<{ logs: RunLogLine[]; selectedStep: string; selectedHost: string }>()

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
  .map(line => toDisplayEntry(runLogToLogEntry(line))))
</script>

<template>
  <section class="host-log-panel">
    <div class="log-breadcrumb">{{ selectedStep || 'All steps' }} · {{ selectedHost || 'All hosts' }}</div>
    <div class="run-log-list">
      <div v-for="line in visible" :key="line.id" class="run-log-row">
        <span class="source-chip">[{{ line.source_id || 'local' }}]</span>
        <span class="log-message">{{ line.message }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.host-log-panel {
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
</style>
