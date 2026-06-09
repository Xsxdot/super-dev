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
import { computed, ref, watch } from 'vue'
import type { Run } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = withDefaults(defineProps<{ runs: Run[]; loading?: boolean; artifactKind?: string; limit?: number }>(), {
  limit: 5,
})
const emit = defineEmits<{ detail: [run: Run]; rollback: [run: Run] }>()
const { t } = useAppI18n()
const showAll = ref(false)
const visibleRuns = computed(() => showAll.value ? props.runs : props.runs.slice(0, props.limit))
const hiddenRunCount = computed(() => Math.max(0, props.runs.length - visibleRuns.value.length))

watch(() => props.runs, () => {
  showAll.value = false
})

function duration(run: Run) {
  if (!run.finished_at || !run.started_at) return '--'
  return `${Math.max(0, Math.round((run.finished_at - run.started_at) / 1000))}s`
}

function failedSteps(run: Run) {
  return (run.step_runs ?? [])
    .filter(step => step.status === 'failed')
    .map(step => step.step_name)
}

function statusLabel(run: Run) {
  return t(`overview.pipeline.status.${run.status}`)
}

function startedAt(run: Run) {
  if (!run.started_at) return '--'
  const value = run.started_at < 1_000_000_000_000 ? run.started_at * 1000 : run.started_at
  const date = new Date(value)
  const pad = (item: number) => String(item).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function summary(run: Run) {
  const failed = failedSteps(run)
  if (failed.length > 0) return t('overview.pipeline.failedSteps', { steps: failed.join(', ') })
  if (run.status === 'running') return t('overview.pipeline.runningSummary')
  if (run.status === 'canceled') return t('overview.pipeline.canceledSummary')
  return '-'
}
</script>

<template>
  <div
    class="run-history"
    data-test="run-history"
    :style="{ '--history-row-count': visibleRuns.length }"
  >
    <div class="run-history-timeline" data-test="run-history-timeline" aria-hidden="true">
      <span
        v-for="run in visibleRuns"
        :key="run.id"
        :class="`status-${run.status}`"
        data-test="run-history-node"
      ></span>
    </div>
    <div class="history-table">
      <div class="history-head">
        <span>{{ t('overview.pipeline.columnStatus') }}</span>
        <span>{{ t('overview.pipeline.columnVersion') }}</span>
        <span>{{ t('overview.pipeline.columnEnv') }}</span>
        <span>{{ t('overview.pipeline.columnStartedAt') }}</span>
        <span>{{ t('overview.pipeline.columnDuration') }}</span>
        <span>{{ t('settings.pipeline.artifactKind') }}</span>
        <span>{{ t('overview.pipeline.columnSummary') }}</span>
        <span>{{ t('overview.pipeline.columnActions') }}</span>
      </div>
      <div v-if="loading" class="history-loading">{{ t('common.loading') }}</div>
      <div v-for="run in visibleRuns" :key="run.id" class="run-item" data-test="run-history-row">
        <div class="run-row">
          <span class="run-status" :class="`status-${run.status}`">
            <span class="status-ring" aria-hidden="true"></span>
            {{ statusLabel(run) }}
          </span>
          <span class="run-version">{{ run.artifact_version || '--' }}</span>
          <span>{{ run.env_name || '--' }}</span>
          <span>{{ startedAt(run) }}</span>
          <span>{{ duration(run) }}</span>
          <span>{{ artifactKind || 'file' }}</span>
          <span
            class="run-summary"
            :class="{ failed: failedSteps(run).length }"
            data-test="run-failed-summary"
            :title="summary(run)"
          >
            {{ summary(run) }}
          </span>
          <span class="run-actions">
            <button type="button" data-test="run-detail" @click="emit('detail', run)">
              {{ run.status === 'running' ? t('overview.pipeline.openLiveConsole') : t('overview.pipeline.openConsole') }}
            </button>
            <button
              type="button"
              data-test="run-rollback"
              :disabled="run.status === 'running'"
              @click="emit('rollback', run)"
            >
              {{ t('overview.pipeline.rollback') }}
            </button>
          </span>
        </div>
      </div>
      <button
        v-if="hiddenRunCount > 0"
        type="button"
        class="history-view-all"
        data-test="run-history-view-all"
        @click="showAll = true"
      >
        {{ t('overview.pipeline.viewAllHistory', { count: props.runs.length }) }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.run-history {
  --history-header-height: 42px;
  --history-row-height: 61px;
  --history-node-size: 17px;
  display: grid;
  grid-template-columns: 54px minmax(0, 1fr);
  margin: 0 12px 16px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  background: rgba(8, 13, 20, 0.72);
  overflow: hidden;
}
.run-history-timeline {
  position: relative;
  display: grid;
  grid-template-rows: repeat(var(--history-row-count), var(--history-row-height));
  align-content: start;
  justify-items: center;
  padding: var(--history-header-height) 0 16px;
}
.run-history-timeline::before {
  position: absolute;
  top: calc(var(--history-header-height) + var(--history-node-size) / 2);
  bottom: calc(16px + var(--history-node-size) / 2);
  left: 50%;
  width: 2px;
  background: var(--text-tertiary);
  content: '';
  opacity: 0.72;
  transform: translateX(-50%);
}
.run-history-timeline span {
  position: relative;
  z-index: 1;
  align-self: center;
  width: var(--history-node-size);
  height: var(--history-node-size);
  border: 2px solid var(--text-tertiary);
  border-radius: 50%;
  background: var(--bg-primary);
}
.run-history-timeline .status-success {
  border-color: var(--status-success);
  background: var(--status-success);
}
.run-history-timeline .status-running,
.run-history-timeline .status-pending {
  border-color: var(--status-starting);
  background: var(--bg-primary);
}
.run-history-timeline .status-failed,
.run-history-timeline .status-canceled {
  border-color: var(--status-failed);
  background: var(--status-failed);
}
.history-table {
  min-width: 0;
  padding: 16px 8px 16px 0;
}
.history-head {
  display: grid;
  grid-template-columns: 96px minmax(130px, 0.9fr) 72px 150px 66px 74px minmax(170px, 1fr) 190px;
  align-items: center;
  gap: 12px;
  min-height: var(--history-header-height);
  padding: 0 12px;
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.history-loading,
.run-row {
  display: grid;
  grid-template-columns: 96px minmax(130px, 0.9fr) 72px 150px 66px 74px minmax(170px, 1fr) 190px;
  align-items: center;
  gap: 12px;
  min-height: var(--history-row-height);
  padding: 7px 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  color: var(--text-secondary);
  font-size: 12px;
  background: rgba(18, 24, 34, 0.64);
}
.run-item {
  margin-top: 0;
}
.run-item + .run-item {
  margin-top: -1px;
}
.run-version {
  overflow: hidden;
  color: var(--text-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 700;
}
.status-ring {
  width: 13px;
  height: 13px;
  border: 2px solid currentColor;
  border-radius: 50%;
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
.run-summary {
  overflow: hidden;
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-summary.failed {
  color: var(--status-failed);
  white-space: nowrap;
}
.run-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
  white-space: nowrap;
}
.run-actions button {
  height: 24px;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: rgba(22, 27, 34, 0.82);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 11px;
  font-weight: 700;
}
.run-actions button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.history-view-all {
  display: block;
  margin: 14px auto 0;
  border: 0;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  font-size: 12px;
  font-weight: 700;
}
.history-view-all:hover {
  text-decoration: underline;
}

@media (max-width: 920px) {
  .run-history {
    grid-template-columns: 1fr;
  }
  .run-history-timeline {
    display: none;
  }
  .history-table {
    padding: 10px;
  }
  .history-head {
    display: none;
  }
  .run-row {
    grid-template-columns: 86px minmax(100px, 1fr) 70px 72px;
  }
  .run-actions {
    grid-column: span 2;
  }
}
</style>
