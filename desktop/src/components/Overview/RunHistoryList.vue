<!--
RunHistoryList：展示单条流水线下的 run 历史。

职责：
  - 渲染状态、版本、环境和耗时
  - 暴露详情和回滚动作

边界：
  - 不执行回滚
  - 不拉取日志
-->
<script setup lang="ts">
import type { Run } from '@/api/agent'

defineProps<{ runs: Run[]; loading?: boolean }>()
const emit = defineEmits<{ detail: [run: Run]; rollback: [run: Run] }>()

function duration(run: Run) {
  if (!run.finished_at || !run.started_at) return '--'
  return `${Math.max(0, Math.round((run.finished_at - run.started_at) / 1000))}s`
}
</script>

<template>
  <div class="run-history">
    <div v-if="loading" class="history-loading">Loading</div>
    <div v-for="run in runs" :key="run.id" class="run-row">
      <span class="run-status">{{ run.status }}</span>
      <span class="run-version">{{ run.artifact_version || '--' }}</span>
      <span>{{ run.env_name || '--' }}</span>
      <span>{{ duration(run) }}</span>
      <button type="button" data-test="run-detail" @click="emit('detail', run)">Detail</button>
      <button type="button" data-test="run-rollback" @click="emit('rollback', run)">Rollback</button>
    </div>
  </div>
</template>

<style scoped>
.run-history {
  margin: 6px 0 12px 38px;
  border-left: 1px solid var(--border-secondary);
}
.history-loading,
.run-row {
  display: grid;
  grid-template-columns: 88px minmax(100px, 1fr) 80px 64px 68px 84px;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 6px 10px;
  color: var(--text-secondary);
  font-size: 12px;
}
.run-version {
  overflow: hidden;
  color: var(--text-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-status {
  color: var(--status-running);
  font-weight: 700;
}
.run-row button {
  height: 24px;
  border: 1px solid var(--border-secondary);
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
  font-size: 11px;
}
</style>
