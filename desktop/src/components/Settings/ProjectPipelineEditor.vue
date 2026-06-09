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
const yamlOpen = ref(false)
const saveError = ref<string | null>(null)
const pipelineForm = ref<InstanceType<typeof SingleProjectPipelineForm> | null>(null)
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

function phaseBlockCount(phase: 'build' | 'deploy' | 'finally') {
  return activePipeline.value?.pipeline?.[phase]?.length ?? 0
}

const railItems = computed(() => {
  const buildCount = phaseBlockCount('build')
  const deployCount = phaseBlockCount('deploy')
  const finallyCount = phaseBlockCount('finally')
  return [
    {
      key: 'basic',
      state: 'done',
      icon: 'i',
      title: t('settings.pipeline.basicInfo'),
      hint: t('settings.pipeline.basicInfoHint'),
      count: '',
    },
    {
      key: 'build',
      state: buildCount > 0 ? 'active' : '',
      icon: '⌁',
      title: t('settings.pipeline.buildPhase'),
      hint: t('settings.pipeline.buildPhaseHint'),
      count: `${buildCount} ${t('settings.pipeline.templateUnit')}`,
    },
    {
      key: 'deploy',
      state: buildCount === 0 && deployCount > 0 ? 'active' : '',
      icon: '↗',
      title: t('settings.pipeline.deployPhase'),
      hint: t('settings.pipeline.deployPhaseHint'),
      count: `${deployCount} ${t('settings.pipeline.templateUnit')}`,
    },
    {
      key: 'finally',
      state: buildCount === 0 && deployCount === 0 && finallyCount > 0 ? 'active' : '',
      icon: '□',
      title: t('settings.pipeline.cleanupPhase'),
      hint: t('settings.pipeline.cleanupPhaseHint'),
      count: `${finallyCount} ${t('settings.pipeline.templateUnit')}`,
    },
    {
      key: 'preview',
      state: 'preview-item',
      icon: '◎',
      title: t('settings.pipeline.previewAndSave'),
      hint: t('settings.pipeline.previewHint'),
      count: '',
    },
  ]
})

const pipelineYaml = computed(() => {
  const pipeline = activePipeline.value
  if (!pipeline) return ''
  const services = (pipeline.services ?? []).map(service => `  - ${service}`).join('\n') || '  []'
  return [
    `id: ${pipeline.id}`,
    `name: ${pipeline.name}`,
    `artifact_kind: ${pipeline.artifact_kind || 'file'}`,
    'services:',
    services,
    'pipeline:',
    `  build: ${pipeline.pipeline?.build?.length ?? 0}`,
    `  deploy: ${pipeline.pipeline?.deploy?.length ?? 0}`,
    `  finally: ${pipeline.pipeline?.finally?.length ?? 0}`,
  ].join('\n')
})

function saveTemplateConfig() {
  pipelineForm.value?.saveTemplateConfig()
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
      <div class="settings-modal-header pipeline-editor-header">
        <div class="pipeline-editor-heading">
          <h2 class="settings-modal-title">{{ t('settings.projects.editPipeline') }} · {{ activePipeline?.name || project.name }}</h2>
          <span class="pipeline-editor-project-badge">{{ project.name }}</span>
        </div>
        <div class="pipeline-editor-header-actions">
          <button type="button" class="settings-btn settings-btn-secondary" data-test="pipeline-editor-yaml" @click="yamlOpen = true">
            {{ t('settings.pipeline.viewYaml') }}
          </button>
          <button
            type="button"
            class="settings-btn settings-btn-primary"
            :disabled="saving"
            @click="save"
          >
            {{ saving ? t('common.loading') : t('common.save') }}
          </button>
          <button
            type="button"
            class="settings-btn settings-btn-icon settings-btn-ghost pipeline-editor-close"
            data-test="pipeline-editor-close"
            @click="emit('cancel')"
          >
            ×
          </button>
        </div>
      </div>

      <div class="settings-modal-body pipeline-editor-content" data-test="pipeline-editor-scroll">
        <ul v-if="errors.length" class="settings-alert settings-alert-danger err-list">
          <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
        </ul>
        <div v-if="saveError" class="settings-alert settings-alert-danger err-list">{{ saveError }}</div>

        <div class="pipeline-editor-shell" data-test="pipeline-editor-shell">
          <div class="pipeline-editor-form-column" data-test="pipeline-editor-form-column">
            <SingleProjectPipelineForm
              v-if="activePipeline"
              ref="pipelineForm"
              with-structure-rail
              :pipeline="activePipeline"
              :services="draft.services"
              :hosts="hosts"
              :templates="pipelineTemplates ?? []"
              :initial-mode="initialMode"
              :on-view-template="viewTemplate"
              @update:pipeline="updateActivePipeline"
            >
              <template #rail>
                <aside class="pipeline-editor-rail" data-test="pipeline-editor-structure">
                  <div class="rail-title">{{ t('settings.pipeline.editorStructure') }}</div>
                  <div
                    v-for="item in railItems"
                    :key="item.key"
                    class="rail-item"
                    :class="item.state"
                    :data-test="`pipeline-editor-rail-${item.key}`"
                  >
                    <span class="rail-icon">{{ item.icon }}</span>
                    <span>
                      <strong>{{ item.title }}</strong>
                      <small>{{ item.hint }}</small>
                    </span>
                    <em v-if="item.count">{{ item.count }}</em>
                  </div>
                </aside>
              </template>
            </SingleProjectPipelineForm>
          </div>
        </div>
      </div>

      <div class="settings-modal-footer pipeline-editor-actions">
        <div class="pipeline-editor-save-note" data-test="pipeline-editor-preview">
          <span aria-hidden="true"></span>
          {{ t('settings.pipeline.requiredComplete') }}
        </div>
        <div class="pipeline-editor-footer-buttons">
          <button type="button" class="settings-btn" data-test="pipeline-config-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</button>
          <button
            type="button"
            class="settings-btn settings-btn-primary"
            data-test="pipeline-config-save-template"
            @click="saveTemplateConfig"
          >
            {{ t('settings.pipeline.saveTemplateConfig') }}
          </button>
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
      </div>

      <TemplateContentModal
        :open="yamlOpen"
        :title="t('settings.pipeline.yamlTitle')"
        :yaml="pipelineYaml"
        @close="yamlOpen = false"
      />

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
  display: grid;
  grid-template-rows: auto minmax(0, 1fr) auto;
  width: min(1480px, calc(100vw - 20px));
  height: calc(100vh - 20px);
  max-height: calc(100vh - 20px);
  background: rgba(13, 18, 26, 0.98);
  overflow: hidden;
}
.pipeline-editor-header {
  min-height: 62px;
  padding: 14px 16px;
}
.pipeline-editor-heading {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.pipeline-editor-heading .settings-modal-title {
  overflow: hidden;
  font-size: 18px;
  font-weight: 800;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pipeline-editor-project-badge {
  border: 1px solid color-mix(in srgb, var(--accent) 42%, transparent);
  border-radius: 5px;
  padding: 4px 8px;
  background: color-mix(in srgb, var(--accent) 22%, transparent);
  color: #9cc2ff;
  font-size: 12px;
  font-weight: 800;
}
.pipeline-editor-header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
.pipeline-editor-close {
  border-color: transparent;
  font-size: 24px;
  line-height: 1;
}
.pipeline-editor-content {
  min-height: 0;
  padding: 0;
  overflow: auto;
}
.pipeline-editor-shell {
  min-width: 0;
  min-height: 100%;
  height: 100%;
}
.pipeline-editor-form-column {
  min-width: 0;
  min-height: 0;
  height: 100%;
}
.pipeline-editor-rail {
  height: 100%;
  border-right: 1px solid var(--border-secondary);
  padding: 12px;
  background: rgba(18, 27, 38, 0.78);
}
.rail-title {
  margin-bottom: 12px;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 700;
}
.rail-item {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  min-height: 62px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  padding: 10px;
  color: var(--text-secondary);
  font-size: 12px;
  background: rgba(13, 18, 26, 0.58);
}
.rail-item + .rail-item {
  margin-top: 8px;
}
.rail-item.active {
  border-color: color-mix(in srgb, var(--accent) 58%, transparent);
  background: color-mix(in srgb, var(--accent) 26%, transparent);
  color: var(--text-primary);
}
.rail-item.done .rail-icon {
  border-color: var(--status-success);
  color: var(--status-success);
}
.rail-item.preview-item {
  grid-template-columns: 28px minmax(0, 1fr) 10px;
}
.rail-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 1px solid var(--border-secondary);
  border-radius: 50%;
  color: var(--text-tertiary);
  font-size: 12px;
  font-style: normal;
  font-weight: 800;
}
.rail-item strong {
  display: block;
  color: var(--text-primary);
  font-size: 13px;
}
.rail-item small {
  display: block;
  margin-top: 3px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.rail-item em {
  border-radius: 5px;
  padding: 4px 7px;
  background: color-mix(in srgb, var(--accent) 22%, transparent);
  color: #9cc2ff;
  font-size: 11px;
  font-style: normal;
  font-weight: 800;
}
.pipeline-editor-actions {
  align-items: center;
  justify-content: space-between;
  min-height: 62px;
  padding: 12px 30px 12px 24px;
  background: rgba(21, 30, 42, 0.96);
}
.pipeline-editor-save-note {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 700;
}
.pipeline-editor-save-note span {
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: var(--status-success);
}
.pipeline-editor-footer-buttons {
  display: flex;
  align-items: center;
  gap: 14px;
}
.err-list {
  margin: 0;
  list-style: none;
}

@media (max-width: 1100px) {
  .pipeline-editor-rail {
    display: none;
  }
}
</style>
