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
import { Icon } from '@iconify/vue'
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

function statusIcon(run: Run) {
  switch (run.status) {
    case 'success':
      return 'lucide:circle-check'
    case 'failed':
      return 'lucide:circle-x'
    case 'running':
    case 'pending':
      return 'lucide:refresh-cw'
    case 'canceled':
      return 'lucide:ban'
    default:
      return 'lucide:circle'
  }
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
      >
        <Icon v-if="run.status === 'success'" icon="lucide:check" aria-hidden="true" />
        <Icon v-else-if="run.status === 'failed'" icon="lucide:x" aria-hidden="true" />
        <Icon v-else-if="run.status === 'running' || run.status === 'pending'" icon="lucide:refresh-cw" aria-hidden="true" />
      </span>
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
            <Icon class="status-icon" :icon="statusIcon(run)" aria-hidden="true" />
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
              <Icon icon="lucide:terminal-square" aria-hidden="true" />
              {{ run.status === 'running' ? t('overview.pipeline.openLiveConsole') : t('overview.pipeline.openConsole') }}
            </button>
            <button type="button" data-test="run-log" @click="emit('detail', run)">
              <Icon icon="lucide:file-text" aria-hidden="true" />
              {{ t('overview.pipeline.logs') }}
            </button>
            <button
              type="button"
              data-test="run-rollback"
              :disabled="run.status === 'running'"
              @click="emit('rollback', run)"
            >
              <Icon icon="lucide:rotate-ccw" aria-hidden="true" />
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
  --history-node-size: 18px;
  display: grid;
  grid-template-columns: 66px minmax(0, 1fr);
  margin: 0;
  padding: 14px 20px 12px 22px;
  border: 0;
  border-radius: 0;
  background: #0b1118;
  overflow: visible;
}
.run-history-timeline {
  position: relative;
  display: grid;
  grid-template-rows: repeat(var(--history-row-count), var(--history-row-height));
  align-content: start;
  justify-items: center;
  width: 36px;
  padding: var(--history-header-height) 0 16px;
}
.run-history-timeline::before {
  position: absolute;
  top: calc(var(--history-header-height) + var(--history-node-size) / 2);
  bottom: calc(16px + var(--history-node-size) / 2);
  left: 50%;
  width: 2px;
  background: #778292;
  content: '';
  transform: translateX(-50%);
}
.run-history-timeline span {
  position: relative;
  z-index: 1;
  display: grid;
  place-items: center;
  align-self: center;
  width: var(--history-node-size);
  height: var(--history-node-size);
  border: 0;
  border-radius: 50%;
  background: #737d8c;
  color: #071018;
}
.run-history-timeline .status-success {
  background: #47d764;
  color: #082310;
}
.run-history-timeline .status-running,
.run-history-timeline .status-pending {
  background: transparent;
  color: #ffbd17;
}
.run-history-timeline .status-failed,
.run-history-timeline .status-canceled {
  background: #ff4b55;
  color: #200508;
}
.run-history-timeline svg {
  width: 14px;
  height: 14px;
}
.history-table {
  min-width: 0;
  padding: 0;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
}
.history-head {
  display: grid;
  grid-template-columns: 94px 140px 64px 150px 72px 76px minmax(150px, 1fr) 201px;
  align-items: center;
  gap: 0;
  height: var(--history-header-height);
  padding: 0;
  background: #0f151e;
  color: var(--text-tertiary);
  font-size: 14px;
  font-weight: 700;
}
.history-head span {
  padding: 0 11px;
}
.history-loading,
.run-row {
  display: grid;
  grid-template-columns: 94px 140px 64px 150px 72px 76px minmax(150px, 1fr) 201px;
  align-items: center;
  gap: 0;
  min-height: 61px;
  padding: 0;
  border: 0;
  border-top: 1px solid var(--border-secondary);
  border-radius: 0;
  color: var(--text-secondary);
  font-size: 14px;
  background: #0b1118;
}
.run-row > span,
.history-loading {
  padding: 0 11px;
}
.run-item {
  margin-top: 0;
}
.run-item + .run-item {
  margin-top: 0;
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
  justify-content: center;
  gap: 5px;
  width: fit-content;
  min-width: 62px;
  height: 30px;
  border: 1px solid transparent;
  border-radius: 6px;
  padding: 0 8px;
  font-weight: 700;
}
.status-icon {
  width: 14px;
  height: 14px;
}
.status-success {
  border-color: rgba(71, 215, 100, 0.22);
  background: rgba(33, 143, 61, 0.16);
  color: #47d764;
}
.status-running,
.status-pending {
  border-color: rgba(255, 189, 23, 0.28);
  background: rgba(210, 153, 19, 0.2);
  color: #ffbd17;
}
.status-failed,
.status-canceled {
  border-color: rgba(255, 75, 85, 0.22);
  background: rgba(223, 54, 64, 0.16);
  color: #ff4b55;
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
  gap: 6px;
  min-width: 0;
  padding: 0 11px;
  white-space: nowrap;
}
.run-actions button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 32px;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: rgba(22, 27, 34, 0.82);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 14px;
  font-weight: 700;
  white-space: nowrap;
}
.run-actions button svg {
  display: none;
}
.run-actions button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.history-view-all {
  display: block;
  margin: 14px auto 10px;
  border: 0;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  font-size: 14px;
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
