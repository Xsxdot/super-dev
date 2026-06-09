<!--
SingleProjectPipelineForm：单条项目流水线表单。

职责：
  - 编辑一条 ProjectPipeline 的基础信息、服务绑定和模板化 pipeline
  - 将变更通过 update:pipeline 返回父组件

边界：
  - 不保存项目配置
  - 不执行流水线或读取运行历史
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { ArtifactKind, Pipeline, PipelineTemplateSummary, ProjectPipeline } from '@/api/agent'
import PipelineTemplateWizard from './PipelineTemplateWizard.vue'

const props = defineProps<{
  pipeline: ProjectPipeline
  services: Array<{ id: string; name: string }>
  hosts: Array<{ id: string; name: string }>
  templates: PipelineTemplateSummary[]
  initialMode?: 'template' | 'blank'
  onViewTemplate?: (template: PipelineTemplateSummary, apply: () => void) => void
}>()

const emit = defineEmits<{ 'update:pipeline': [ProjectPipeline] }>()
const { t } = useAppI18n()
const draft = ref<ProjectPipeline>({ ...props.pipeline, services: [...(props.pipeline.services ?? [])] })

watch(() => props.pipeline, (pipeline) => {
  draft.value = { ...pipeline, services: [...(pipeline.services ?? [])] }
})

function patch(partial: Partial<ProjectPipeline>) {
  draft.value = {
    ...draft.value,
    ...partial,
    services: partial.services ? [...partial.services] : [...(draft.value.services ?? [])],
  }
  emit('update:pipeline', draft.value)
}

function toggleService(name: string, checked: boolean) {
  const set = new Set(draft.value.services ?? [])
  if (checked) set.add(name)
  else set.delete(name)
  patch({ services: [...set] })
}

function setPipeline(pipeline: Pipeline | undefined) {
  patch({ pipeline: pipeline ?? {} })
}
</script>

<template>
  <section class="single-pipeline-form">
    <div class="single-pipeline-topbar" data-test="pipeline-editor-basics">
      <div class="topbar-field name-field">
        <label class="field-row">
          <span>{{ t('settings.pipeline.name') }}</span>
          <input
            class="settings-input"
            data-test="single-pipeline-name"
            :value="draft.name"
            @input="patch({ name: ($event.target as HTMLInputElement).value })"
          />
        </label>
      </div>

      <div class="topbar-field">
        <label class="field-row">
          <span>{{ t('settings.pipeline.artifactKind') }}</span>
          <div class="artifact-segment" data-test="single-pipeline-artifact-kind">
            <button
              v-for="kind in (['file', 'image'] as ArtifactKind[])"
              :key="kind"
              type="button"
              :class="{ active: (draft.artifact_kind || 'file') === kind }"
              @click="patch({ artifact_kind: kind })"
            >
              {{ kind }}
            </button>
          </div>
        </label>
      </div>

      <div class="topbar-field services-field">
        <div class="field-row">
          <span>{{ t('settings.pipeline.services') }}</span>
          <div class="service-list">
            <label v-for="service in services" :key="service.id || service.name" class="service-item">
              <input
                type="checkbox"
                :data-test="`single-pipeline-service-${service.name}`"
                :checked="(draft.services ?? []).includes(service.name)"
                @change="toggleService(service.name, ($event.target as HTMLInputElement).checked)"
              />
              {{ service.name }}
            </label>
          </div>
        </div>
      </div>
    </div>

    <div class="single-pipeline-main">
      <PipelineTemplateWizard
        :model-value="draft.pipeline"
        :templates="templates"
        :hosts="hosts"
        :initial-mode="initialMode"
        :on-view-template="onViewTemplate"
        @update:model-value="setPipeline"
      />
    </div>
  </section>
</template>

<style scoped>
.single-pipeline-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}
.single-pipeline-topbar {
  display: grid;
  grid-template-columns: minmax(240px, 1fr) 210px minmax(360px, 1.4fr);
  gap: 14px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  padding: 10px 12px;
  background: color-mix(in srgb, var(--bg-elevated) 88%, transparent);
}
.single-pipeline-main {
  display: grid;
  gap: 12px;
  min-width: 0;
}
.field-row {
  display: grid;
  grid-template-columns: 1fr;
  align-items: start;
  gap: 6px;
  color: var(--text-secondary);
  font-size: 12px;
}
.service-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 12px;
}
.service-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 12px;
}
.artifact-segment {
  display: grid;
  grid-template-columns: 1fr 1fr;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
}
.artifact-segment button {
  height: 30px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
}
.artifact-segment button + button {
  border-left: 1px solid var(--border-secondary);
}
.artifact-segment button.active {
  background: var(--accent);
  color: #fff;
  font-weight: 700;
}

@media (max-width: 1040px) {
  .single-pipeline-topbar {
    grid-template-columns: 1fr;
  }
  .field-row {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
}
</style>
