<!--
FailureBanner：运行控制台失败诊断横幅。

职责：
  - 从 run.step_runs 中找出首个失败 task
  - 展示失败 step、host 别名和 exit code
  - 发出查看日志事件，由页面/store 负责选择 step+host

边界：
  - 不读取日志正文
  - 不执行重试、回滚或其它控制动作
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { Run, RunTask, StepRun } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ run: Run | null }>()
const emit = defineEmits<{ 'view-logs': [step: string, host: string] }>()
const { t } = useAppI18n()

interface FailedTaskView {
  stepName: string
  hostID: string
  hostName: string
  exitCode: number | string
}

function failedTaskForStep(step: StepRun): RunTask | undefined {
  return step.tasks.find(task => task.status === 'failed')
}

const failed = computed<FailedTaskView | null>(() => {
  if (!props.run) return null
  for (const step of props.run.step_runs) {
    const task = failedTaskForStep(step)
    if (!task) continue
    return {
      stepName: step.step_name,
      hostID: task.host_id || '',
      hostName: task.host_name || task.host_id || 'local',
      exitCode: task.exit_code ?? '-',
    }
  }
  return null
})

function viewLogs() {
  if (!failed.value) return
  emit('view-logs', failed.value.stepName, failed.value.hostID)
}
</script>

<template>
  <section v-if="failed" class="failure-banner" role="alert">
    <div class="failure-copy">
      <strong>{{ t('runConsole.failureTitle') }}</strong>
      <span>{{ t('runConsole.failureDetail', { step: failed.stepName, host: failed.hostName, code: failed.exitCode }) }}</span>
    </div>
    <button type="button" data-test="view-failure-logs" class="failure-action" @click="viewLogs">
      {{ t('runConsole.viewLogs') }}
    </button>
  </section>
</template>

<style scoped>
.failure-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 9px 16px;
  border-bottom: 1px solid color-mix(in srgb, var(--status-failed) 35%, var(--border-secondary));
  background: color-mix(in srgb, var(--status-failed) 10%, var(--bg-primary));
  color: var(--text-primary);
}
.failure-copy {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 12px;
}
.failure-copy strong {
  color: var(--status-failed);
}
.failure-action {
  flex: 0 0 auto;
  min-height: 28px;
  padding: 0 10px;
  border: 1px solid color-mix(in srgb, var(--status-failed) 45%, var(--border-secondary));
  border-radius: 5px;
  background: var(--bg-primary);
  color: var(--status-failed);
  cursor: pointer;
  font-size: 12px;
  font-weight: 700;
}
</style>
