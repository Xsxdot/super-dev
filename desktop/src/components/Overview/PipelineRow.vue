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

defineProps<{ pipeline: ProjectPipeline; expanded: boolean }>()
const emit = defineEmits<{ run: []; edit: []; toggle: [] }>()
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
    <button type="button" data-test="pipeline-run" class="primary-action" @click="emit('run')">Run</button>
    <button type="button" data-test="pipeline-edit" class="text-action" @click="emit('edit')">Edit</button>
  </div>
</template>

<style scoped>
.pipeline-row {
  display: grid;
  grid-template-columns: 28px minmax(160px, 1fr) 76px 76px;
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
