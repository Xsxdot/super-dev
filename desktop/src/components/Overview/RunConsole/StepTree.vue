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
import type { StepRun } from '@/api/agent'

defineProps<{ steps: StepRun[]; selectedStep: string; selectedHost: string }>()
const emit = defineEmits<{ 'select-step': [step: string]; 'select-host': [step: string, host: string] }>()
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
        <span class="step-status">{{ step.status }}</span>
        <span class="step-name">{{ step.step_name }}</span>
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
        <span class="host-status">{{ task.status }}</span>
        <span class="host-name">{{ task.host_name || task.host_id || 'local' }}</span>
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
  grid-template-columns: 74px minmax(0, 1fr);
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
.step-status,
.host-status {
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.step-name,
.host-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
