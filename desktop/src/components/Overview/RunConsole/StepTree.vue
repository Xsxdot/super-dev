<!--
StepTree：运行控制台左侧步骤和主机导航。

职责：
  - 按 step_runs 展示步骤状态
  - 展示每个步骤下的主机任务状态
  - 发出步骤或主机选择事件

边界：
  - 不拉取 run 数据
  - 不渲染日志正文
-->
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { RunStatus, RunTask, StepRun } from '@/api/agent'
import { formatDuration } from '@/lib/timeDisplay'

defineProps<{ steps: StepRun[]; selectedStep: string; selectedHost: string }>()
const emit = defineEmits<{ 'select-step': [step: string]; 'select-host': [step: string, host: string] }>()

const nowMs = ref(Date.now())
let timer: ReturnType<typeof window.setInterval> | undefined

onMounted(() => {
  timer = window.setInterval(() => {
    nowMs.value = Date.now()
  }, 1000)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})

function statusSymbol(status: RunStatus) {
  if (status === 'running') return ''
  if (status === 'success') return '✓'
  if (status === 'failed') return '×'
  if (status === 'skipped') return '−'
  return '•'
}

function elapsedMs(startedAt?: number, finishedAt?: number, status?: RunStatus): number | null {
  if (!startedAt) return null
  const end = finishedAt || (status === 'running' ? nowMs.value : 0)
  if (!end) return null
  return Math.max(0, end - startedAt)
}

function taskDuration(task: RunTask): string {
  return formatDuration(elapsedMs(task.started_at, task.finished_at, task.status))
}

function stepDuration(step: StepRun): string {
  const started = step.tasks
    .map(task => task.started_at)
    .filter((value): value is number => !!value)
  if (started.length === 0) return ''
  const start = Math.min(...started)
  const running = step.tasks.some(task => task.status === 'running')
  const finished = step.tasks
    .map(task => task.finished_at)
    .filter((value): value is number => !!value)
  const end = running ? nowMs.value : Math.max(...finished, 0)
  if (!end) return ''
  return formatDuration(Math.max(0, end - start))
}
</script>

<template>
  <nav class="step-tree">
    <div v-for="step in steps" :key="step.step_name" class="step-group">
      <button
        type="button"
        data-test="step-select"
        class="step-item"
        :class="{ selected: selectedStep === step.step_name && !selectedHost }"
        @click="emit('select-step', step.step_name)"
      >
        <span class="status-icon" :class="step.status" :aria-label="step.status">{{ statusSymbol(step.status) }}</span>
        <span class="step-name">{{ step.step_name }}</span>
        <span v-if="stepDuration(step)" data-test="step-duration" class="duration-chip">{{ stepDuration(step) }}</span>
      </button>
      <button
        v-for="task in step.tasks"
        :key="task.host_id || task.host_name || 'local'"
        type="button"
        class="host-item"
        :class="{ selected: selectedStep === step.step_name && selectedHost === (task.host_id || '') }"
        :data-test="`host-select-${task.host_id || 'local'}`"
        @click="emit('select-host', step.step_name, task.host_id || '')"
      >
        <span class="status-icon" :class="task.status" :aria-label="task.status">{{ statusSymbol(task.status) }}</span>
        <span class="host-name">{{ task.host_name || task.host_id || 'local' }}</span>
        <span v-if="taskDuration(task)" data-test="host-duration" class="duration-chip">{{ taskDuration(task) }}</span>
      </button>
    </div>
  </nav>
</template>

<style scoped>
.step-tree {
  min-width: 220px;
  padding: 10px;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-primary);
  overflow: auto;
}
.step-group + .step-group {
  margin-top: 8px;
}
.step-item,
.host-item {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 30px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  text-align: left;
}
.step-item {
  border-radius: 5px;
  color: var(--text-primary);
  font-weight: 700;
}
.host-item {
  padding-left: 14px;
  border-radius: 5px;
}
.step-item:hover,
.host-item:hover,
.selected {
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.status-icon {
  width: 14px;
  height: 14px;
  border-radius: 999px;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 14px;
  text-align: center;
}
.status-icon.running {
  border: 2px solid color-mix(in srgb, var(--accent) 25%, transparent);
  border-top-color: var(--accent);
  animation: spin 0.9s linear infinite;
}
.status-icon.success {
  color: var(--status-running);
}
.status-icon.failed {
  color: var(--status-failed);
}
.status-icon.skipped,
.status-icon.pending {
  color: var(--text-tertiary);
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
.step-name,
.host-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.duration-chip {
  color: var(--text-tertiary);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
</style>
