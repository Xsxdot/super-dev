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
import { useAppI18n } from '@/i18n/useAppI18n'

defineProps<{ runs: Run[]; loading?: boolean }>()
const emit = defineEmits<{ detail: [run: Run]; rollback: [run: Run] }>()
const { t } = useAppI18n()

function duration(run: Run) {
  if (!run.finished_at || !run.started_at) return '--'
  return `${Math.max(0, Math.round((run.finished_at - run.started_at) / 1000))}s`
}

function failedSteps(run: Run) {
  return (run.step_runs ?? [])
    .filter(step => step.status === 'failed')
    .map(step => step.step_name)
}
</script>

<template>
  <div class="run-history" data-test="run-history">
    <div class="history-head">
      <span>{{ t('overview.pipeline.historyTitle') }}</span>
    </div>
    <div v-if="loading" class="history-loading">{{ t('common.loading') }}</div>
    <div v-for="run in runs" :key="run.id" class="run-item">
      <div class="run-row">
        <span class="run-status" :class="`status-${run.status}`">{{ run.status }}</span>
        <span class="run-version">{{ run.artifact_version || '--' }}</span>
        <span>{{ run.env_name || '--' }}</span>
        <span>{{ duration(run) }}</span>
        <button type="button" data-test="run-detail" @click="emit('detail', run)">{{ t('overview.pipeline.openConsole') }}</button>
        <button
          type="button"
          data-test="run-rollback"
          :disabled="run.status === 'running'"
          @click="emit('rollback', run)"
        >
          {{ t('overview.pipeline.rollback') }}
        </button>
      </div>
      <div v-if="failedSteps(run).length" class="run-failed-summary" data-test="run-failed-summary">
        {{ t('overview.pipeline.failedSteps', { steps: failedSteps(run).join(', ') }) }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.run-history {
  margin: -1px 0 14px 42px;
  border: 1px solid var(--border-secondary);
  border-top: 0;
  border-radius: 0 0 8px 8px;
  background: color-mix(in srgb, var(--bg-elevated) 88%, transparent);
  overflow: hidden;
}
.history-head {
  display: flex;
  align-items: center;
  min-height: 34px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 700;
}
.history-loading,
.run-row {
  display: grid;
  grid-template-columns: 100px minmax(120px, 1fr) 90px 72px 96px 96px;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 7px 12px;
  color: var(--text-secondary);
  font-size: 12px;
}
.run-item + .run-item {
  border-top: 1px solid var(--border-secondary);
}
.run-version {
  overflow: hidden;
  color: var(--text-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-status {
  font-weight: 700;
}
.status-success {
  color: var(--status-success);
}
.status-running,
.status-pending {
  color: var(--status-running);
}
.status-failed,
.status-canceled {
  color: var(--status-failed);
}
.run-row button {
  height: 24px;
  border: 1px solid var(--border-secondary);
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
  font-size: 11px;
}
.run-row button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.run-failed-summary {
  margin: 0 12px 10px 112px;
  border: 1px solid color-mix(in srgb, var(--status-failed) 34%, transparent);
  border-radius: 6px;
  padding: 7px 9px;
  background: color-mix(in srgb, var(--status-failed) 8%, transparent);
  color: var(--status-failed);
  font-size: 12px;
}

@media (max-width: 920px) {
  .run-history {
    margin-left: 0;
  }
  .run-row {
    grid-template-columns: 86px minmax(100px, 1fr) 70px 72px;
  }
  .run-row button {
    grid-column: span 2;
  }
  .run-failed-summary {
    margin-left: 12px;
  }
}
</style>
