<!--
ProjectPipelinePanel：项目级流水线编辑面板。

职责：
  - 展示和编辑 project.pipelines 列表
  - 为单条项目流水线配置名称、默认服务和模板化 pipeline
  - 将修改后的 pipelines 整体 emit 给父组件

边界：
  - 不预览或执行流水线
  - 不直接保存项目配置
-->
<script setup lang="ts">
import type { ProjectPipeline, Pipeline, PipelineTemplateSummary } from '@/api/agent'
import PipelineTemplateWizard from './PipelineTemplateWizard.vue'

const props = defineProps<{
  modelValue: ProjectPipeline[]
  services: Array<{ id: string; name: string }>
  hosts: Array<{ id: string; name: string }>
  templates: PipelineTemplateSummary[]
}>()

const emit = defineEmits<{ 'update:modelValue': [ProjectPipeline[]] }>()

function patch(index: number, partial: Partial<ProjectPipeline>) {
  const next = props.modelValue.map((item, i) => (i === index ? { ...item, ...partial } : item))
  emit('update:modelValue', next)
}

function addPipeline() {
  const id = `pipeline-${props.modelValue.length + 1}`
  emit('update:modelValue', [
    ...props.modelValue,
    { id, name: 'Deploy', services: [], pipeline: {} as Pipeline },
  ])
}

function removePipeline(index: number) {
  emit('update:modelValue', props.modelValue.filter((_, i) => i !== index))
}

function setPipeline(index: number, pipeline: Pipeline | undefined) {
  patch(index, { pipeline: pipeline ?? {} })
}
</script>

<template>
  <section class="project-pipelines">
    <div class="section-head">
      <div class="section-title">项目流水线</div>
      <button type="button" class="add-btn" data-test="add-project-pipeline" @click="addPipeline">新增流水线</button>
    </div>

    <div v-if="modelValue.length === 0" class="pipeline-empty">还没有项目级流水线</div>

    <div v-for="(item, index) in modelValue" :key="item.id || index" class="pipeline-item">
      <input
        class="pipeline-name"
        data-test="project-pipeline-name"
        :value="item.name"
        @input="patch(index, { name: ($event.target as HTMLInputElement).value })"
      />

      <div class="service-list">
        <label v-for="service in services" :key="service.id || service.name" class="service-item">
          <input
            type="checkbox"
            :checked="(item.services ?? []).includes(service.name)"
            @change="patch(index, {
              services: ($event.target as HTMLInputElement).checked
                ? [...(item.services ?? []), service.name]
                : (item.services ?? []).filter(name => name !== service.name),
            })"
          />
          {{ service.name }}
        </label>
      </div>

      <PipelineTemplateWizard
        :model-value="item.pipeline"
        :templates="templates"
        :hosts="hosts"
        @update:model-value="setPipeline(index, $event)"
      />

      <div class="pipeline-actions">
        <button
          type="button"
          class="text-btn"
          data-test="project-pipeline-save-template"
          @click="patch(index, { pipeline: item.pipeline })"
        >
          保存流水线草稿
        </button>
        <button type="button" class="danger-btn" @click="removePipeline(index)">删除流水线</button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.project-pipelines {
  border-top: 1px solid var(--border-secondary);
  margin-top: 12px;
  padding-top: 12px;
}
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.section-title {
  font-size: 12px;
  color: var(--text-secondary);
}
.add-btn {
  padding: 3px 9px;
  font-size: 11px;
  color: #fff;
  background: var(--accent);
  border: 1px solid var(--accent);
  cursor: pointer;
}
.pipeline-empty {
  margin-top: 8px;
  font-size: 11px;
  color: var(--text-tertiary);
}
.pipeline-item {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 8px;
  margin-top: 8px;
}
.pipeline-name {
  width: 100%;
  box-sizing: border-box;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-secondary);
  padding: 4px 8px;
  font-size: 12px;
}
.service-list {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  margin: 8px 0;
}
.service-item {
  font-size: 12px;
  color: var(--text-secondary);
}
.pipeline-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.text-btn,
.danger-btn {
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 11px;
  padding: 0;
}
.text-btn {
  color: var(--accent);
}
.danger-btn {
  color: var(--status-failed);
}
</style>
