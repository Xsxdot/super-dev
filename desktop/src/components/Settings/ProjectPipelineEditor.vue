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
import { ref, onMounted } from 'vue'
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
import ProjectPipelinePanel from './ProjectPipelinePanel.vue'
import TemplateContentModal from './TemplateContentModal.vue'

const props = defineProps<{
  project: Project
  pipelineTemplates?: PipelineTemplateSummary[]
  initialMode?: 'template' | 'blank'
}>()
const emit = defineEmits<{ saved: [Project]; cancel: [] }>()

const agentStore = useAgentStore()
const { t } = useI18n()
const draft = ref(projectToDraft(props.project))
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

function updatePipelines(pipelines: ProjectPipeline[]) {
  draft.value.pipelines = pipelines
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
  return draft.value.pipelines[0]?.id || 'pipeline-1'
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
      service_names: draft.value.pipelines[0]?.services ?? [],
      variables: draft.value.pipelines[0]?.variables,
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
  <div class="pipeline-editor-backdrop" @click.self="emit('cancel')">
    <div class="pipeline-editor-body">
      <div class="pipeline-editor-title">{{ t('settings.projects.editPipeline') }} · {{ project.name }}</div>

      <ul v-if="errors.length" class="err-list">
        <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
      </ul>
      <div v-if="saveError" class="err-list">{{ saveError }}</div>

      <ProjectPipelinePanel
        :model-value="draft.pipelines"
        :services="draft.services"
        :hosts="hosts"
        :templates="pipelineTemplates ?? []"
        :initial-mode="initialMode"
        :on-view-template="viewTemplate"
        @update:model-value="updatePipelines"
      />

      <div class="pipeline-editor-actions">
        <button type="button" data-test="pipeline-config-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button type="button" class="primary" data-test="pipeline-config-save" :disabled="saving" @click="save">
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
        <div v-if="applyPreviewError" class="err-list">{{ applyPreviewError }}</div>
        <PipelinePreview v-if="applyPreview" :preview="applyPreview" />
      </TemplateContentModal>
    </div>
  </div>
</template>

<style scoped>
.pipeline-editor-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.pipeline-editor-body {
  width: min(820px, calc(100vw - 32px));
  max-height: 88vh;
  overflow-y: auto;
  padding: 20px 22px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.pipeline-editor-title {
  margin-bottom: 14px;
  font-size: 14px;
  font-weight: 600;
}
.err-list {
  margin: 0 0 12px;
  padding: 8px 12px;
  list-style: none;
  background: var(--bg-secondary);
  border-left: 2px solid var(--status-failed);
  color: var(--status-failed);
  font-size: 12px;
}
.pipeline-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--border-secondary);
}
.pipeline-editor-actions button {
  padding: 5px 14px;
  font-size: 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
}
.pipeline-editor-actions button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
.pipeline-editor-actions button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
