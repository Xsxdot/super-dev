<!--
ProjectPipelineEditor：项目流水线独立编辑器。

职责：
  - 持有项目配置草稿，仅编辑 project.pipelines
  - 加载主机摘要，提供给流水线目标选择
  - 保存：校验项目流水线 → 拍平为 SetupPayload → PUT /setup → reloadProject → emit saved

边界：
  - 不编辑环境、服务、运行时、日志或项目变量
  - 不执行流水线，不新增后端接口
-->
<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  api,
  type PipelinePreviewResponse,
  type PipelineTemplateDetail,
  type PipelineTemplateSummary,
  type Project,
  type ProjectPipeline,
} from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { projectToDraft, draftToPayload, validateDraftDetailed, formatValidationIssue } from '@/lib/configDraft'
import PipelinePreview from './PipelinePreview.vue'
import SingleProjectPipelineForm from './SingleProjectPipelineForm.vue'
import TemplateContentModal from './TemplateContentModal.vue'

const props = defineProps<{
  project: Project
  pipelineTemplates?: PipelineTemplateSummary[]
  initialMode?: 'template' | 'blank'
  pipelineId?: string
}>()
const emit = defineEmits<{ saved: [Project]; cancel: [] }>()

const agentStore = useAgentStore()
const { t } = useI18n()
const draft = ref(projectToDraft(props.project))
if (draft.value.pipelines.length === 0) {
  draft.value.pipelines = [{ id: 'pipeline-1', name: 'Deploy', services: [], artifact_kind: 'file', pipeline: {} }]
}
const activePipelineId = ref(props.pipelineId ?? draft.value.pipelines[0]?.id ?? '')
const activePipeline = computed(() =>
  draft.value.pipelines.find(pipeline => pipeline.id === activePipelineId.value) ?? draft.value.pipelines[0],
)
const hosts = ref<Array<{ id: string; name: string }>>([])
const errors = ref<string[]>([])
const saving = ref(false)
const saveError = ref<string | null>(null)
const selectedTemplate = ref<PipelineTemplateSummary | null>(null)
const templateDetail = ref<PipelineTemplateDetail | null>(null)
const templateLoading = ref(false)
const templateError = ref('')
const templateModalOpen = ref(false)
const applyPreview = ref<PipelinePreviewResponse | null>(null)
const applyPreviewError = ref('')
const applyingTemplate = ref(false)
const applyTemplateDraft = ref<(() => void) | null>(null)

onMounted(async () => {
  try {
    const list = await api.listHosts()
    hosts.value = list.map(h => ({ id: h.id, name: h.name }))
  } catch {
    hosts.value = []
  }
})

function updateActivePipeline(next: ProjectPipeline) {
  const exists = draft.value.pipelines.some(pipeline => pipeline.id === next.id)
  if (!exists) {
    draft.value.pipelines = [...draft.value.pipelines, next]
    activePipelineId.value = next.id
    return
  }
  draft.value.pipelines = draft.value.pipelines.map(pipeline => pipeline.id === next.id ? next : pipeline)
}

function pipelineValidationErrors(): string[] {
  // 流水线编辑器只处理 project.pipelines；服务配置错误留给「编辑配置」入口处理。
  return validateDraftDetailed(draft.value)
    .filter(error => error.scope === 'pipeline')
    .map(formatValidationIssue)
}

function defaultEnvName() {
  return props.project.environments?.find(env => env.is_dev)?.name ?? props.project.environments?.[0]?.name ?? 'dev'
}

function firstPipelineId() {
  return activePipeline.value?.id || 'pipeline-1'
}

async function viewTemplate(template: PipelineTemplateSummary, apply: () => void) {
  selectedTemplate.value = template
  applyTemplateDraft.value = apply
  templateDetail.value = null
  applyPreview.value = null
  applyPreviewError.value = ''
  templateError.value = ''
  templateModalOpen.value = true
  templateLoading.value = true
  try {
    templateDetail.value = await api.getPipelineTemplate(template.source, template.id, template.version)
  } catch (e) {
    templateError.value = e instanceof Error ? e.message : String(e)
  } finally {
    templateLoading.value = false
  }
}

async function applyViewedTemplate() {
  applyTemplateDraft.value?.()
  applyingTemplate.value = true
  applyPreview.value = null
  applyPreviewError.value = ''
  try {
    applyPreview.value = await api.previewProjectPipeline(props.project.id, firstPipelineId(), {
      env_name: defaultEnvName(),
      service_names: activePipeline.value?.services ?? [],
      variables: activePipeline.value?.variables,
    })
  } catch (e) {
    applyPreviewError.value = e instanceof Error ? e.message : t('settings.pipeline.applyPreviewFailed')
  } finally {
    applyingTemplate.value = false
  }
}

async function save() {
  errors.value = pipelineValidationErrors()
  if (errors.value.length) return
  saving.value = true
  saveError.value = null
  try {
    const updated = await api.putProjectSetup(props.project.id, draftToPayload(draft.value))
    await agentStore.reloadProject(props.project.id)
    emit('saved', updated)
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : t('common.saveFailed')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="settings-modal-backdrop" @click.self="emit('cancel')">
    <div class="settings-modal settings-modal-wide pipeline-editor-body">
      <div class="settings-modal-header">
        <h2 class="settings-modal-title">{{ t('settings.projects.editPipeline') }} · {{ project.name }}</h2>
      </div>

      <div class="settings-modal-body pipeline-editor-content">
        <ul v-if="errors.length" class="settings-alert settings-alert-danger err-list">
          <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
        </ul>
        <div v-if="saveError" class="settings-alert settings-alert-danger err-list">{{ saveError }}</div>

        <div class="pipeline-editor-shell">
          <aside class="pipeline-editor-rail" data-test="pipeline-editor-structure">
            <div class="rail-title">{{ t('settings.pipeline.editorStructure') }}</div>
            <div class="rail-item active">{{ t('settings.pipeline.basicInfo') }}</div>
            <div class="rail-item">{{ t('settings.pipeline.buildPhase') }}</div>
            <div class="rail-item">{{ t('settings.pipeline.deployPhase') }}</div>
            <div class="rail-item">{{ t('settings.pipeline.cleanupPhase') }}</div>
            <div class="rail-item">{{ t('settings.pipeline.previewAndSave') }}</div>
          </aside>

          <SingleProjectPipelineForm
            v-if="activePipeline"
            :pipeline="activePipeline"
            :services="draft.services"
            :hosts="hosts"
            :templates="pipelineTemplates ?? []"
            :initial-mode="initialMode"
            :on-view-template="viewTemplate"
            @update:pipeline="updateActivePipeline"
          />
        </div>
      </div>

      <div class="settings-modal-footer pipeline-editor-actions">
        <button type="button" class="settings-btn" data-test="pipeline-config-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button
          type="button"
          class="settings-btn settings-btn-primary"
          data-test="pipeline-config-save"
          :disabled="saving"
          @click="save"
        >
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </div>

      <TemplateContentModal
        :open="templateModalOpen"
        :title="selectedTemplate?.name ?? t('settings.templates.contentTitle')"
        :yaml="templateDetail?.yaml ?? ''"
        :detail="templateDetail"
        :loading="templateLoading"
        :error="templateError"
        :can-apply="Boolean(templateDetail)"
        :applying="applyingTemplate"
        @apply="applyViewedTemplate"
        @close="templateModalOpen = false"
      >
        <div v-if="applyPreviewError" class="settings-alert settings-alert-danger err-list">{{ applyPreviewError }}</div>
        <PipelinePreview v-if="applyPreview" :preview="applyPreview" />
      </TemplateContentModal>
    </div>
  </div>
</template>

<style scoped>
.pipeline-editor-body {
  width: min(1380px, calc(100vw - 32px));
  max-height: calc(100vh - 40px);
}
.pipeline-editor-content {
  display: grid;
  gap: 12px;
}
.pipeline-editor-shell {
  display: grid;
  grid-template-columns: 170px minmax(0, 1fr);
  gap: 14px;
  min-width: 0;
}
.pipeline-editor-rail {
  position: sticky;
  top: 0;
  align-self: start;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  padding: 10px;
  background: var(--bg-elevated);
}
.rail-title {
  margin-bottom: 8px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
}
.rail-item {
  border-radius: 6px;
  padding: 8px 9px;
  color: var(--text-secondary);
  font-size: 12px;
}
.rail-item + .rail-item {
  margin-top: 4px;
}
.rail-item.active {
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  color: var(--accent);
  font-weight: 700;
}
.err-list {
  margin: 0;
  list-style: none;
}

@media (max-width: 1040px) {
  .pipeline-editor-shell {
    grid-template-columns: 1fr;
  }
  .pipeline-editor-rail {
    position: static;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .rail-title {
    width: 100%;
    margin-bottom: 0;
  }
  .rail-item + .rail-item {
    margin-top: 0;
  }
}
</style>
