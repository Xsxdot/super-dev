<!--
PipelineRow：项目概览页的一条流水线行。

职责：
  - 展示流水线名称、制品类型和主要操作
  - 将执行、编辑、展开历史动作交给父组件

边界：
  - 不直接调用 API
  - 不渲染 run 历史详情
-->
<script setup lang="ts">
import type { ProjectPipeline } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

defineProps<{ pipeline: ProjectPipeline; expanded: boolean; runningRun?: { id: string } | null }>()
const emit = defineEmits<{ run: []; edit: []; toggle: []; 'open-running': [] }>()
const { t } = useAppI18n()
</script>

<template>
  <div class="pipeline-row">
    <button type="button" data-test="pipeline-expand" class="icon-btn" @click="emit('toggle')">
      {{ expanded ? 'v' : '>' }}
    </button>
    <div class="pipeline-main">
      <div class="pipeline-name">{{ pipeline.name }}</div>
      <div class="pipeline-meta">{{ pipeline.artifact_kind || 'file' }}</div>
    </div>
    <button
      v-if="runningRun"
      type="button"
      data-test="pipeline-running"
      class="running-badge"
      @click="emit('open-running')"
    >
      {{ t('overview.pipeline.running') }}
    </button>
    <button type="button" data-test="pipeline-run" class="primary-action" @click="emit('run')">{{ t('overview.pipeline.run') }}</button>
    <button type="button" data-test="pipeline-edit" class="text-action" @click="emit('edit')">{{ t('overview.pipeline.edit') }}</button>
  </div>
</template>

<style scoped>
.pipeline-row {
  display: grid;
  grid-template-columns: 28px minmax(160px, 1fr) minmax(72px, auto) 76px 76px;
  align-items: center;
  gap: 10px;
  min-height: 52px;
  padding: 8px 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
}
.icon-btn,
.primary-action,
.text-action {
  height: 28px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.icon-btn {
  width: 28px;
  padding: 0;
}
.primary-action {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
  font-weight: 700;
}
.text-action {
  background: transparent;
}
.running-badge {
  height: 24px;
  border: 1px solid color-mix(in srgb, var(--accent) 50%, transparent);
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  color: var(--accent);
  cursor: pointer;
  font-size: 11px;
  font-weight: 700;
}
.pipeline-main {
  min-width: 0;
}
.pipeline-name {
  overflow: hidden;
  font-size: 13px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pipeline-meta {
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 11px;
}
</style>
