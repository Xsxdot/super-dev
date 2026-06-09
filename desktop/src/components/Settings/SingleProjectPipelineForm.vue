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
  withStructureRail?: boolean
  hidePreviewStrip?: boolean
  onViewTemplate?: (template: PipelineTemplateSummary, apply: () => void) => void
}>()

const emit = defineEmits<{ 'update:pipeline': [ProjectPipeline] }>()
const { t } = useAppI18n()
const draft = ref<ProjectPipeline>({ ...props.pipeline, services: [...(props.pipeline.services ?? [])] })
const wizard = ref<InstanceType<typeof PipelineTemplateWizard> | null>(null)

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

function saveDraft() {
  wizard.value?.saveTemplate()
}

defineExpose({ saveDraft })
</script>

<template>
  <section class="single-pipeline-form" :class="{ 'with-structure-rail': withStructureRail }">
    <div class="single-pipeline-topbar" data-test="single-pipeline-form-topbar">
      <div class="topbar-field name-field">
        <label class="field-row">
          <span>{{ t('settings.pipeline.name') }} / Pipeline name</span>
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
          <span>{{ t('settings.pipeline.artifactKind') }} / Artifact kind</span>
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
          <span>{{ t('settings.pipeline.services') }} / Services</span>
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

    <div class="single-pipeline-lower">
      <div v-if="withStructureRail" class="single-pipeline-rail-slot">
        <slot name="rail" />
      </div>
      <div class="single-pipeline-main" data-test="pipeline-editor-stage-area">
        <PipelineTemplateWizard
          ref="wizard"
          :model-value="draft.pipeline"
          :pipeline-roles="draft.roles ?? pipeline.roles"
          :templates="templates"
          :hosts="hosts"
          :initial-mode="initialMode"
          :hide-preview-strip="hidePreviewStrip"
          :on-view-template="onViewTemplate"
          @update:model-value="setPipeline"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.single-pipeline-form {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  min-width: 0;
  min-height: 0;
  height: 100%;
}
.single-pipeline-topbar {
  display: grid;
  grid-template-columns: 370px 206px minmax(0, 1fr);
  align-items: start;
  gap: 22px;
  min-height: 98px;
  border: 0;
  border-bottom: 1px solid #263240;
  border-radius: 0;
  padding: 18px 18px 16px;
  background: #121922;
}
.with-structure-rail .single-pipeline-topbar {
  border-top: 0;
  border-right: 0;
  border-left: 0;
  border-radius: 0;
}
.single-pipeline-lower {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: 0;
  height: 100%;
}
.with-structure-rail .single-pipeline-lower {
  grid-template-columns: 290px minmax(0, 1fr);
}
.single-pipeline-rail-slot {
  min-width: 0;
  min-height: 0;
}
.single-pipeline-main {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.field-row {
  display: grid;
  grid-template-columns: 1fr;
  align-items: start;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 500;
}
.service-list {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 18px;
  min-height: 38px;
  align-items: center;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  padding: 0 12px;
  background: #0b1118;
}
.service-item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 500;
}
.service-item input,
.target-item input,
.boolean-field input {
  accent-color: var(--accent);
}
.artifact-segment {
  display: grid;
  grid-template-columns: 1fr 1fr;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  overflow: hidden;
  background: #0b1118;
}
.artifact-segment button {
  height: 38px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}
.artifact-segment button + button {
  border-left: 1px solid var(--border-secondary);
}
.artifact-segment button.active {
  background: linear-gradient(180deg, #2587ff, #176de9);
  color: #fff;
  font-weight: 600;
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
@media (max-width: 1100px) {
  .with-structure-rail .single-pipeline-lower {
    grid-template-columns: 1fr;
  }
  .single-pipeline-rail-slot {
    display: none;
  }
}
</style>
