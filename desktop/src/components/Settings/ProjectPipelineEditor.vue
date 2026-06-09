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
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'
import {
  api,
  type PipelinePreviewResponse,
  type PipelineTemplateDetail,
  type PipelineTemplateSummary,
  type PipelinePhase,
  type PipelineStep,
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
// 全屏预览弹层开关：顶部「预览执行图」按钮触发，替代旧的底部预览窄带。
const previewModalOpen = ref(false)
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
const editorPreview = ref<PipelinePreviewResponse | null>(null)
const editorPreviewError = ref('')
const previewPhases: PipelinePhase[] = ['build', 'deploy', 'finally']
type PreviewStepRun = PipelinePreviewResponse['run']['step_runs'][number]

onMounted(async () => {
  window.addEventListener('keydown', onPreviewKeydown)
  void loadEditorPreview()
  try {
    const list = await api.listHosts()
    hosts.value = list.map(h => ({ id: h.id, name: h.name }))
  } catch {
    hosts.value = []
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onPreviewKeydown)
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

function phaseLabel(phase: PipelinePhase) {
  return t(`settings.pipeline.phases.${phase}`)
}

function phaseDisplayLabel(phase: PipelinePhase) {
  return `${phaseLabel(phase).replace(/\s*Phase$/, '').replace(/阶段$/, '')} ${phase}`
}

function previewTarget(step: PipelineStep) {
  const role = step.roles?.[0]
  if (!role) return t('common.local')
  const target = activePipeline.value?.pipeline?.roles?.[role]?.[0]
  return target ? hostDisplayName(target) : role
}

function phaseIcon(phase: PipelinePhase) {
  if (phase === 'deploy') return 'lucide:server'
  if (phase === 'finally') return 'lucide:shield-check'
  return 'lucide:package'
}

function hasPreviewablePipeline() {
  const pipeline = activePipeline.value?.pipeline
  return previewPhases.some(phase => (pipeline?.[phase] ?? []).length > 0)
}

function compiledPreviewTarget(step: PreviewStepRun) {
  const task = step.tasks.find(item => item.host_name || item.host_id)
  return task?.host_name || task?.host_id || t('common.local')
}

function hostDisplayName(hostID: string) {
  return hosts.value.find(host => host.id === hostID)?.name ?? hostID
}

function phaseRunnerTarget(phase: PipelinePhase) {
  const pipeline = activePipeline.value?.pipeline
  for (const [index, step] of (pipeline?.[phase] ?? []).entries()) {
    for (const role of step.roles ?? []) {
      const target = pipeline?.roles?.[role]?.[0]
      if (target) return hostDisplayName(target)
    }
    const conventionRole = `${phase}_${index}_runner`
    const conventionTarget = activePipeline.value?.roles?.[conventionRole]?.hosts?.[0]
    if (conventionTarget) return hostDisplayName(conventionTarget)
  }
  if (phase === 'build') return hosts.value[0]?.name
  return undefined
}

function compactStepName(name: string) {
  const parts = name.split('.')
  return parts[parts.length - 1] || name
}

function previewNodeFromStep(step: PreviewStepRun, index: number, targetFallback?: string) {
  const target = compiledPreviewTarget(step)
  return {
    id: `${step.phase}-${index}-${step.step_name}`,
    phase: step.phase,
    name: compactStepName(step.step_name),
    target: target === t('common.local') && targetFallback ? targetFallback : target,
    icon: phaseIcon(step.phase),
  }
}

function compactCompiledPreviewNodes(steps: PreviewStepRun[]) {
  const buildTarget = phaseRunnerTarget('build')
  const buildNodes = steps
    .filter(step => step.phase === 'build')
    .map((step, index) => previewNodeFromStep(step, index, buildTarget))
    .slice(0, 3)
  const deploySteps = steps.filter(step => step.phase === 'deploy')
  const deployTarget = deploySteps
    .map(step => compiledPreviewTarget(step))
    .find(target => target !== t('common.local'))
  const healthStep = deploySteps.find(step => /health|check/i.test(step.step_name))
  const deployNodes = deploySteps.length > 0
    ? [
        {
        id: 'deploy-summary',
        phase: 'deploy' as PipelinePhase,
        name: 'Deploy',
        target: deployTarget || compiledPreviewTarget(deploySteps[0]),
        icon: phaseIcon('deploy'),
        },
        ...(healthStep
          ? [{
              id: 'deploy-health-check',
              phase: 'finally' as PipelinePhase,
              name: 'Health Check',
              target: (compiledPreviewTarget(healthStep) === t('common.local') ? deployTarget : compiledPreviewTarget(healthStep)) || deployTarget || compiledPreviewTarget(deploySteps[0]),
              icon: phaseIcon('finally'),
            }]
          : []),
      ]
    : []
  const remaining = Math.max(0, 5 - buildNodes.length - deployNodes.length)
  const finallyNodes = steps
    .filter(step => step.phase === 'finally')
    .map((step, index) => previewNodeFromStep(step, index, deployTarget))
    .slice(0, remaining)
  return [...buildNodes, ...deployNodes, ...finallyNodes].slice(0, 5)
}

const localPreviewNodes = computed(() => {
  const pipeline = activePipeline.value?.pipeline
  if (!pipeline) return []
  return previewPhases
    .flatMap(phase => (pipeline[phase] ?? []).map((step, index) => ({
      id: `${phase}-${index}-${step.name}`,
      phase,
      name: step.name || phaseLabel(phase),
      target: previewTarget(step),
      icon: phaseIcon(phase),
    })))
    .slice(0, 6)
})

const compiledPreviewNodes = computed(() =>
  compactCompiledPreviewNodes(editorPreview.value?.run.step_runs ?? []),
)

const editorPreviewNodes = computed(() =>
  compiledPreviewNodes.value.length > localPreviewNodes.value.length
    ? compiledPreviewNodes.value
    : localPreviewNodes.value,
)

const railItems = computed(() => {
  const buildCount = phaseBlockCount('build')
  const deployCount = phaseBlockCount('deploy')
  const finallyCount = phaseBlockCount('finally')
  return [
    {
      key: 'basic',
      state: 'done',
      icon: 'lucide:info',
      title: t('settings.pipeline.basicInfo'),
      hint: t('settings.pipeline.basicInfoHint'),
      count: '',
    },
    {
      key: 'build',
      state: buildCount > 0 ? 'active' : '',
      icon: 'lucide:wrench',
      title: phaseDisplayLabel('build'),
      hint: t('settings.pipeline.buildPhaseHint'),
      count: `${buildCount} ${t('settings.pipeline.templateUnit')}`,
    },
    {
      key: 'deploy',
      state: buildCount === 0 && deployCount > 0 ? 'active' : '',
      icon: 'lucide:rocket',
      title: phaseDisplayLabel('deploy'),
      hint: t('settings.pipeline.deployPhaseHint'),
      count: `${deployCount} ${t('settings.pipeline.templateUnit')}`,
    },
    {
      key: 'finally',
      state: buildCount === 0 && deployCount === 0 && finallyCount > 0 ? 'active' : '',
      icon: 'lucide:trash-2',
      title: phaseDisplayLabel('finally'),
      hint: t('settings.pipeline.cleanupPhaseHint'),
      count: `${finallyCount} ${t('settings.pipeline.templateUnit')}`,
    },
    {
      key: 'preview',
      state: 'preview-item',
      icon: 'lucide:eye',
      title: t('settings.pipeline.previewAndSave'),
      hint: t('settings.pipeline.previewHint'),
      count: '',
    },
  ]
})

async function loadEditorPreview() {
  const pipeline = activePipeline.value
  if (!pipeline || !hasPreviewablePipeline()) return
  editorPreviewError.value = ''
  editorPreview.value = null
  try {
    editorPreview.value = await api.previewProjectPipeline(props.project.id, pipeline.id, {
      env_name: defaultEnvName(),
      service_names: pipeline.services ?? [],
      variables: pipeline.variables,
    })
  } catch (e) {
    editorPreviewError.value = e instanceof Error ? e.message : t('settings.pipeline.applyPreviewFailed')
  }
}

function openPreviewModal() {
  previewModalOpen.value = true
  void loadEditorPreview()
}

function onPreviewKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') previewModalOpen.value = false
}

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
            <Icon icon="lucide:code-2" aria-hidden="true" />
            {{ t('settings.pipeline.viewYaml') }}
          </button>
          <button
            type="button"
            class="settings-btn settings-btn-secondary"
            data-test="pipeline-preview-open"
            :data-preview-count="editorPreviewNodes.length"
            @click="openPreviewModal"
          >
            {{ t('settings.pipeline.previewGraph') }}
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
            <Icon icon="lucide:x" aria-hidden="true" />
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
              hide-preview-strip
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
                    <span class="rail-icon"><Icon :icon="item.icon" aria-hidden="true" /></span>
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
        <div class="pipeline-editor-footer-status">
          <Icon icon="lucide:circle-check" aria-hidden="true" />
          {{ t('settings.pipeline.requiredFieldsComplete') }} · 2 {{ t('settings.pipeline.stageUnit') }} · {{ t('settings.pipeline.savedPreviewHint') }}
          <Icon icon="lucide:circle-help" aria-hidden="true" />
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

      <div
        v-if="previewModalOpen"
        class="pipeline-preview-overlay"
        data-test="pipeline-preview-overlay"
        @click.self="previewModalOpen = false"
      >
        <div class="pipeline-preview-modal">
          <header class="preview-modal-head">
            <h3>
              {{ t('settings.pipeline.previewGraph') }}
              <span class="preview-modal-sub">· {{ t('settings.pipeline.previewFromDraft') }}</span>
            </h3>
            <button
              type="button"
              class="preview-modal-close"
              data-test="pipeline-preview-close"
              @click="previewModalOpen = false"
            >
              ×
            </button>
          </header>
          <div class="preview-modal-body">
            <div class="preview-flow" data-test="pipeline-preview-flow">
              <template v-for="(node, i) in editorPreviewNodes" :key="node.id">
                <div class="preview-node">
                  <div class="preview-node-phase">{{ phaseLabel(node.phase) }}</div>
                  <div class="preview-node-name">{{ node.name }}</div>
                  <div class="preview-node-target">{{ node.target }}</div>
                </div>
                <div v-if="i < editorPreviewNodes.length - 1" class="preview-conn">→</div>
              </template>
              <div v-if="editorPreviewNodes.length === 0" class="preview-empty">
                {{ t('settings.pipeline.requiredComplete') }}
              </div>
            </div>
            <div v-if="editorPreviewError" class="preview-error">{{ editorPreviewError }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
:global(.settings-modal.pipeline-editor-body) {
  width: min(1487px, calc(100vw - 20px));
  max-width: none;
  height: calc(100vh - 20px);
  max-height: calc(100vh - 20px);
  border-radius: 12px;
}

:global(.settings-modal-backdrop:has(.pipeline-editor-body)) {
  padding: 10px;
  background: rgba(2, 5, 8, 0.72);
}

.pipeline-editor-body {
  display: grid;
  grid-template-rows: 62px minmax(0, 1fr) 66px;
  background: linear-gradient(180deg, #121922 0%, #111820 100%);
  overflow: hidden;
}
.pipeline-editor-header {
  grid-column: 1;
  min-height: 62px;
  padding: 0 28px 0 16px;
  border-bottom: 1px solid #263240;
}
.pipeline-editor-heading {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.pipeline-editor-heading .settings-modal-title {
  overflow: hidden;
  font-size: 20px;
  font-weight: 800;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pipeline-editor-project-badge {
  min-width: 31px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 6px;
  padding: 0 8px;
  background: #0b3c85;
  color: #7bb2ff;
  font-size: 14px;
  font-weight: 800;
}
.pipeline-editor-header-actions {
  display: flex;
  align-items: center;
  gap: 18px;
}
.pipeline-editor-header-actions .settings-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
}
.pipeline-editor-header-actions .settings-btn svg {
  width: 15px;
  height: 15px;
}
.pipeline-editor-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-color: transparent;
}
.pipeline-editor-content {
  grid-column: 1;
  grid-row: 2;
  min-height: 0;
  padding: 0;
  overflow: hidden;
  scrollbar-color: rgba(139, 148, 158, 0.38) rgba(13, 18, 26, 0.72);
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
  border-right: 1px solid #263240;
  padding: 12px 10px;
  background: #111820;
}
.rail-title {
  margin: 0 0 12px 4px;
  color: var(--text-primary);
  font-size: 16px;
  font-weight: 700;
}
.rail-item {
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  min-height: 64px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 0 12px;
  color: var(--text-secondary);
  font-size: 14px;
  background: #121923;
}
.rail-item + .rail-item {
  margin-top: 8px;
}
.rail-item.active {
  border-color: #1f7bff;
  background: linear-gradient(135deg, rgba(31, 123, 255, 0.78), rgba(15, 94, 216, 0.72));
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
  width: 25px;
  height: 25px;
  border: 1px solid var(--border-secondary);
  border-radius: 50%;
  color: var(--text-tertiary);
  font-size: 12px;
  font-style: normal;
  font-weight: 800;
}
.rail-icon svg {
  width: 14px;
  height: 14px;
}
.rail-item strong {
  display: block;
  color: var(--text-primary);
  font-size: 14px;
}
.rail-item small {
  display: block;
  margin-top: 5px;
  color: var(--text-tertiary);
  font-size: 12px;
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
  position: relative;
  z-index: 2;
  grid-column: 1;
  grid-row: 3;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  min-height: 66px;
  gap: 0;
  padding: 0;
  border-top: 1px solid #263240;
  background: #141c25;
  pointer-events: auto;
}
.pipeline-editor-footer-buttons {
  grid-column: 2;
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: flex-end;
  gap: 16px;
  padding: 0 30px 14px 0;
  pointer-events: auto;
}
.pipeline-editor-footer-status {
  grid-column: 1;
  display: flex;
  align-items: center;
  gap: 7px;
  padding-left: 24px;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 650;
  pointer-events: auto;
}
.pipeline-editor-footer-status svg:first-child {
  color: #47d764;
}
.pipeline-preview-overlay {
  position: fixed;
  z-index: 70;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(5, 7, 11, 0.72);
}
.pipeline-preview-modal {
  display: flex;
  width: min(1100px, 92vw);
  max-height: 86vh;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--border-secondary);
  border-radius: 12px;
  background: var(--bg-secondary);
}
.preview-modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--border-secondary);
  padding: 18px 24px;
}
.preview-modal-head h3 {
  margin: 0;
  color: var(--text-primary);
  font-size: 17px;
  font-weight: 800;
}
.preview-modal-sub {
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 500;
}
.preview-modal-close {
  border: 0;
  background: none;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 20px;
  line-height: 1;
}
.preview-modal-body {
  overflow: auto;
  padding: 34px 28px;
}
.preview-flow {
  display: flex;
  align-items: stretch;
  flex-wrap: wrap;
  gap: 0;
}
.preview-node {
  min-width: 190px;
  flex: 1;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  padding: 18px;
  background: var(--bg-tertiary);
}
.preview-node-phase {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.preview-node-name {
  margin: 8px 0 10px;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 700;
}
.preview-node-target {
  color: var(--text-tertiary);
  font-family: ui-monospace, Menlo, monospace;
  font-size: 11px;
}
.preview-conn {
  display: flex;
  width: 42px;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 18px;
}
.preview-empty {
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 700;
}
.preview-error {
  margin-top: 14px;
  color: var(--status-failed);
  font-size: 12px;
}
.err-list {
  margin: 0;
  list-style: none;
}

@media (max-width: 1100px) {
  .pipeline-editor-rail {
    display: none;
  }
  .pipeline-editor-actions {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
