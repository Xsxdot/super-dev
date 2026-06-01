<!--
PipelinePreview：展示后端编译后的流水线 DAG 预览。

职责：
  - 按 phase 展示步骤、依赖和状态
  - 展示每个步骤将作用到的目标主机

边界：
  - 不计算 DAG
  - 不触发执行或预览请求
  - 不修改 deployment 配置
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { PipelinePhase, PipelinePreviewResponse } from '@/api/agent'

const props = defineProps<{
  preview: PipelinePreviewResponse
}>()

type StepRun = PipelinePreviewResponse['run']['step_runs'][number]

const phaseOrder: PipelinePhase[] = ['build', 'deploy', 'finally']
const { t } = useAppI18n()

const grouped = computed(() => phaseOrder
  .map(phase => ({
    phase,
    steps: props.preview.run.step_runs.filter(step => step.phase === phase),
  }))
  .filter(group => group.steps.length > 0))

function taskTarget(step: StepRun) {
  const names = step.tasks.map(task => task.host_name || task.host_id).filter(Boolean)
  return names.length > 0 ? names.join(', ') : t('common.local')
}
</script>

<template>
  <section class="pipeline-preview">
    <header class="preview-head">
      <span>{{ t('settings.pipeline.preview') }}</span>
      <span class="preview-status">{{ preview.run.status }}</span>
    </header>

    <div v-for="group in grouped" :key="group.phase" class="phase-block">
      <div class="phase-title">{{ group.phase }}</div>
      <article v-for="step in group.steps" :key="`${group.phase}-${step.step_name}`" class="step-row">
        <div class="step-main">
          <span class="step-name">{{ step.step_name }}</span>
          <span class="step-type">{{ step.type }}</span>
          <span class="step-status">{{ step.status }}</span>
        </div>
        <div class="step-meta">
          <span v-if="step.needs?.length">{{ t('settings.pipeline.dependency', { items: step.needs.join(', ') }) }}</span>
          <span>{{ t('settings.pipeline.target', { target: taskTarget(step) }) }}</span>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.pipeline-preview {
  margin-top: 10px;
  border-top: 1px solid var(--border-secondary);
  padding-top: 8px;
}
.preview-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
.preview-status,
.step-status {
  color: var(--text-tertiary);
  font-size: 11px;
}
.phase-block {
  margin-bottom: 8px;
}
.phase-title {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-bottom: 4px;
  text-transform: lowercase;
}
.step-row {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 6px 8px;
  margin-bottom: 4px;
  background: var(--bg-secondary);
}
.step-main {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 12px;
}
.step-name {
  color: var(--text-primary);
  font-weight: 600;
}
.step-type {
  color: var(--text-tertiary);
  font-size: 11px;
}
.step-meta {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 4px;
  color: var(--text-tertiary);
  font-size: 11px;
}
</style>
