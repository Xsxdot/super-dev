<!--
PipelineTemplateWizard：模板化流水线组合编辑器。

职责：
  - 无 pipeline 时提供「配置流水线」入口
  - 按 build/deploy/finally 阶段组合多个模板 include
  - 把 target_role 输入保存为运行组变量名引用
  - 展示后端展开后的流水线预览

边界：
  - 不解析模板 YAML
  - 不直接调用模板/预览 API
  - 不编辑展开后的具体 DAG 步骤
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Icon } from '@iconify/vue'
import type {
  Pipeline,
  PipelinePhase,
  PipelinePreviewResponse,
  ProjectPipelineRole,
  PipelineTemplateCategory,
  PipelineTemplateSummary,
  TemplateFileItem,
  TemplateInput,
} from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import PipelinePreview from './PipelinePreview.vue'

type TemplateVarValue = string | TemplateFileItem[]

type TemplateBlock = {
  id: string
  phase: PipelinePhase
  selectedKey: string
  vars: Record<string, TemplateVarValue>
}

type HostOption = { id: string; name: string }
type WizardStation = PipelinePhase

const props = defineProps<{
  modelValue?: Pipeline
  templates: PipelineTemplateSummary[]
  hosts?: HostOption[]
  preview?: PipelinePreviewResponse
  pipelineRoles?: Record<string, ProjectPipelineRole>
  previewError?: string
  onViewTemplate?: (template: PipelineTemplateSummary, apply: () => void) => void
  initialMode?: 'template' | 'blank'
  hidePreviewStrip?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [Pipeline | undefined] }>()

const phases: PipelinePhase[] = ['build', 'deploy', 'finally']
const phaseCategory: Record<PipelinePhase, PipelineTemplateCategory> = {
  build: 'build',
  deploy: 'deploy',
  finally: 'cleanup',
}
const enabled = ref(Boolean(props.modelValue) || props.initialMode === 'template')
const blocks = ref<TemplateBlock[]>([])
const activeBlockId = ref<string | null>(null)
const activePhase = ref<PipelinePhase>('build')
const activeStation = ref<WizardStation>('build')
const draggingBlockId = ref<string | null>(null)
const nextBlockId = ref(0)
const { t } = useAppI18n()

const canSave = computed(() => blocks.value.length > 0 && blocks.value.every(block => {
  const template = selectedFor(block)
  if (!template) return false
  return Object.entries(template.inputs ?? {}).every(([name, input]) => inputSatisfied(block, name, input))
}))
const activeBlock = computed(() => blocks.value.find(block => block.id === activeBlockId.value) ?? blocks.value[0] ?? null)
const activeBlockInputs = computed<[string, TemplateInput][]>(() => {
  const block = activeBlock.value
  const template = block ? selectedFor(block) : null
  return Object.entries(template?.inputs ?? {})
})
const previewBlocks = computed(() =>
  blocks.value
    .map(block => ({
      id: block.id,
      phase: block.phase,
      name: selectedFor(block)?.name || phaseLabel(block.phase),
      target: t('common.local'),
      icon: phaseIcon(block.phase),
    }))
    .slice(0, 5),
)

watch(() => [props.modelValue, props.pipelineRoles] as const, ([value]) => {
  enabled.value = Boolean(value) || enabled.value || props.initialMode === 'template'
  hydrateFromPipeline(value)
}, { immediate: true })

function templateKey(template: PipelineTemplateSummary) {
  return `${template.source}://${template.id}@${template.version}`
}

function selectedFor(block: TemplateBlock) {
  return props.templates.find(t => templateKey(t) === block.selectedKey)
}

function templateCategory(template: PipelineTemplateSummary): PipelineTemplateCategory {
  return template.category ?? 'general'
}

function templateFitsPhase(template: PipelineTemplateSummary, phase: PipelinePhase) {
  const category = templateCategory(template)
  return category === 'general' || category === phaseCategory[phase]
}

function templatesForBlock(block: TemplateBlock) {
  const options = props.templates.filter(template => templateFitsPhase(template, block.phase))
  const selected = selectedFor(block)
  if (selected && !options.some(template => templateKey(template) === templateKey(selected))) {
    return [selected, ...options]
  }
  return options
}

function blocksForPhase(phase: PipelinePhase) {
  return blocks.value.filter(block => block.phase === phase)
}

function phaseLabel(phase: PipelinePhase) {
  return t(`settings.pipeline.phases.${phase}`)
}

function phaseDisplayLabel(phase: PipelinePhase) {
  return `${phaseLabel(phase).replace(/\s*Phase$/, '').replace(/阶段$/, '')} ${phase}`
}

function phaseIcon(phase: PipelinePhase) {
  if (phase === 'deploy') return 'lucide:server'
  if (phase === 'finally') return 'lucide:shield-check'
  return 'lucide:package'
}

function addBlock(phase: PipelinePhase) {
  const block = { id: String(nextBlockId.value++), phase, selectedKey: '', vars: {} }
  blocks.value.push(block)
  activeBlockId.value = block.id
  activePhase.value = phase
  activeStation.value = phase
}

function removeBlock(block: TemplateBlock) {
	blocks.value = blocks.value.filter(item => item !== block)
	if (activeBlockId.value === block.id) {
		activeBlockId.value = blocks.value[0]?.id ?? null
	}
}

function startBlockDrag(block: TemplateBlock) {
  draggingBlockId.value = block.id
}

function finishBlockDrag() {
  draggingBlockId.value = null
}

function dropBlock(target: TemplateBlock) {
  const sourceId = draggingBlockId.value
  if (!sourceId || sourceId === target.id) return
  const sourceIndex = blocks.value.findIndex(block => block.id === sourceId)
  const targetIndex = blocks.value.findIndex(block => block.id === target.id)
  const source = blocks.value[sourceIndex]
  if (!source || targetIndex < 0 || source.phase !== target.phase) return

  const next = [...blocks.value]
  next.splice(sourceIndex, 1)
  next.splice(targetIndex, 0, source)
  blocks.value = next
  activeBlockId.value = source.id
  activePhase.value = source.phase
  activeStation.value = source.phase
}

function resetBlockInputs(block: TemplateBlock) {
  const template = selectedFor(block)
  block.vars = {}
  for (const [name, input] of Object.entries(template?.inputs ?? {})) {
    if (input.type === 'file_list') {
      block.vars[name] = []
      continue
    }
    block.vars[name] = input.default ?? ''
  }
}

function hydrateFromPipeline(pipeline?: Pipeline) {
  blocks.value = []
  activeBlockId.value = null
	activePhase.value = 'build'
	activeStation.value = 'build'
	nextBlockId.value = 0
	if (!pipeline) return
  for (const phase of phases) {
    for (const step of pipeline[phase] ?? []) {
      if (step.type !== 'include' || !step.with) continue
      const templateURI = typeof step.with.template === 'string' ? step.with.template : ''
      const version = typeof step.with.version === 'string' ? step.with.version : ''
      const rawVars = step.with.vars && typeof step.with.vars === 'object'
        ? step.with.vars as Record<string, unknown>
        : {}
      const selectedKey = version ? `${templateURI}@${version}` : ''
      const template = props.templates.find(t => templateKey(t) === selectedKey)
      const vars = hydrateVars(rawVars, template)
      const block: TemplateBlock = {
        id: String(nextBlockId.value++),
        phase,
        selectedKey,
        vars,
      }
      blocks.value.push(block)
      if (!activeBlockId.value) {
        activeBlockId.value = block.id
        activePhase.value = phase
        activeStation.value = phase
      }
    }
  }
}

function enable() {
  enabled.value = true
  activeStation.value = 'build'
}

function disable() {
  enabled.value = false
  blocks.value = []
  activePhase.value = 'build'
  activeStation.value = 'build'
  emit('update:modelValue', undefined)
}

function inputSatisfied(block: TemplateBlock, name: string, input: TemplateInput) {
  if (input.type === 'file_list') {
    const files = fileItems(block, name)
    if (files.length > 0 && !files.every(file => file.from.trim() !== '' && file.to.trim() !== '')) return false
    return !input.required || files.length > 0
  }
  return !input.required || stringVar(block, name).trim() !== ''
}

function stringVar(block: TemplateBlock, name: string) {
  const value = block.vars[name]
  return typeof value === 'string' ? value : ''
}

function setStringVar(block: TemplateBlock, name: string, value: string) {
  block.vars[name] = value
}

function isBooleanInput(input: TemplateInput) {
  return input.type === 'bool' || input.type === 'boolean'
}

function boolVar(block: TemplateBlock, name: string) {
  return stringVar(block, name) === 'true'
}

function setBoolVar(block: TemplateBlock, name: string, checked: boolean) {
  block.vars[name] = checked ? 'true' : 'false'
}

function fileItems(block: TemplateBlock, name: string) {
  const value = block.vars[name]
  return Array.isArray(value) ? value : []
}

function addFileItem(block: TemplateBlock, name: string) {
  const items = [...fileItems(block, name), { from: '', to: '' }]
  block.vars[name] = items
}

function updateFileItem(block: TemplateBlock, name: string, index: number, field: keyof TemplateFileItem, value: string) {
  const items = fileItems(block, name).map(item => ({ ...item }))
  if (!items[index]) return
  items[index][field] = value
  block.vars[name] = items
}

function removeFileItem(block: TemplateBlock, name: string, index: number) {
  block.vars[name] = fileItems(block, name).filter((_, i) => i !== index)
}

function selectBlock(block: TemplateBlock) {
  activeBlockId.value = block.id
  activePhase.value = block.phase
  activeStation.value = block.phase
}

function selectPhase(phase: PipelinePhase) {
  activePhase.value = phase
  activeStation.value = phase
  const firstBlock = blocksForPhase(phase)[0]
  if (firstBlock) selectBlock(firstBlock)
}

function selectStation(station: WizardStation) {
  selectPhase(station)
}

function hydrateVars(rawVars: Record<string, unknown>, template?: PipelineTemplateSummary): Record<string, TemplateVarValue> {
  const vars: Record<string, TemplateVarValue> = {}
  for (const [key, value] of Object.entries(rawVars)) {
    if (template?.inputs?.[key]?.type === 'file_list' || Array.isArray(value)) {
      vars[key] = normalizeFileList(value)
      continue
    }
    vars[key] = String(value)
  }
  return vars
}

function normalizeFileList(value: unknown): TemplateFileItem[] {
  if (!Array.isArray(value)) return []
  return value
    .filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object' && !Array.isArray(item))
    .map(item => ({ from: String(item.from ?? ''), to: String(item.to ?? '') }))
}

function viewSelected(block: TemplateBlock) {
  const template = selectedFor(block)
  if (template) props.onViewTemplate?.(template, saveTemplate)
}

function saveTemplate() {
  if (!canSave.value) return
  const pipeline: Pipeline = { build: [], deploy: [], finally: [], roles: {}, variables: {} }
  for (const block of blocks.value) {
    const template = selectedFor(block)
    if (!template) continue
    const vars: Record<string, TemplateVarValue> = { ...block.vars }
    for (const [name, input] of Object.entries(template.inputs ?? {})) {
      if (input.type === 'file_list' && fileItems(block, name).length === 0 && !input.required) {
        delete vars[name]
      }
    }
    if (typeof vars.app_name === 'string' && vars.app_name && !pipeline.variables!.app_name) {
      pipeline.variables!.app_name = vars.app_name
    }
    pipeline[block.phase]!.push({
      name: template.name,
      type: 'include',
      with: {
        template: `${template.source}://${template.id}`,
        version: template.version,
        digest: template.digest,
        vars,
      },
    })
  }
  if (Object.keys(pipeline.roles ?? {}).length === 0) delete pipeline.roles
  if (Object.keys(pipeline.variables ?? {}).length === 0) delete pipeline.variables
  emit('update:modelValue', pipeline)
}

defineExpose({ saveTemplate })
</script>

<template>
  <div class="pipeline-wizard">
    <button v-if="!enabled" type="button" class="settings-btn settings-btn-secondary pipeline-enable" data-test="pipeline-enable" @click="enable">
      {{ t('settings.pipeline.configure') }}
    </button>

    <template v-else>
      <div class="wizard-head">
        <span>{{ t('settings.pipeline.template') }}</span>
        <button type="button" class="settings-btn settings-btn-danger pipeline-disable" @click="disable">
          {{ t('settings.pipeline.removeTemplate') }}
        </button>
      </div>

      <div class="phase-tabs" data-test="pipeline-phase-tabs">
        <button
          v-for="(phase, index) in phases"
          :key="phase"
          type="button"
          :class="{ active: activeStation === phase }"
          :data-test="`pipeline-phase-tab-${phase}`"
          @click="selectStation(phase)"
        >
          <span class="station-token">{{ index + 1 }}</span>
          {{ phaseDisplayLabel(phase) }}
        </button>
      </div>

        <div class="wizard-layout">
          <div class="wizard-main" data-test="pipeline-wizard-canvas">
            <div v-if="templates.length === 0" class="pipeline-empty">
              {{ t('settings.pipeline.emptyTemplates') }}
            </div>
            <section :key="activePhase" class="phase-section">
              <header class="phase-head">
                <span>{{ phaseDisplayLabel(activePhase) }} · {{ blocksForPhase(activePhase).length }} {{ t('settings.pipeline.stepCount') }}</span>
                <button type="button" class="settings-btn settings-btn-text text-btn" :data-test="`add-template-${activePhase}`" @click="addBlock(activePhase)">
                  {{ t('settings.pipeline.addTemplate') }}
                </button>
              </header>

              <div v-if="blocksForPhase(activePhase).length === 0" class="phase-empty">{{ t('settings.pipeline.noTemplate') }}</div>

              <div
                v-for="block in blocksForPhase(activePhase)"
                :key="block.id"
                class="template-block"
                :class="{ active: activeBlock?.id === block.id, dragging: draggingBlockId === block.id }"
                draggable="true"
                @click="selectBlock(block)"
                @dragstart="startBlockDrag(block)"
                @dragend="finishBlockDrag"
                @dragover.prevent
                @drop.prevent="dropBlock(block)"
              >
                <div class="block-row">
                  <span class="block-grip" aria-hidden="true">⋮⋮</span>
                  <span class="block-cube" aria-hidden="true"><Icon :icon="phaseIcon(block.phase)" /></span>
                  <span class="block-order">#{{ blocksForPhase(activePhase).indexOf(block) + 1 }}</span>
                  <select
                    v-model="block.selectedKey"
                    class="settings-select field-input block-name"
                    :data-test="`block-${block.id}-template-select`"
                    :title="selectedFor(block)?.name"
                    @change="resetBlockInputs(block); selectBlock(block)"
                    @click.stop
                  >
                    <option value="" disabled>{{ t('settings.pipeline.selectTemplate') }}</option>
                    <option v-for="template in templatesForBlock(block)" :key="templateKey(template)" :value="templateKey(template)">
                      {{ template.name }} · {{ template.source }} · {{ template.version }}
                    </option>
                  </select>
                  <button
                    type="button"
                    class="settings-btn settings-btn-text text-btn"
                    :data-test="`block-${block.id}-view-template`"
                    :disabled="!selectedFor(block)"
                    @click.stop="viewSelected(block)"
                  >
                    {{ t('settings.pipeline.viewTemplate') }}
                  </button>
                  <button type="button" class="settings-btn settings-btn-danger danger-btn" @click.stop="removeBlock(block)">
                    {{ t('common.remove') }}
                  </button>
                </div>

                <div v-if="selectedFor(block)?.description" class="template-description">
                  {{ selectedFor(block)?.description }}
                </div>
              </div>
            </section>
          </div>

          <aside class="wizard-detail-panel" data-test="pipeline-wizard-detail">
            <template v-if="activeBlock && selectedFor(activeBlock)">
              <div class="detail-title">{{ t('settings.pipeline.dynamicInputs') }}</div>
              <div class="detail-subtitle" :title="selectedFor(activeBlock)?.name">{{ t('settings.pipeline.currentTemplate', { name: selectedFor(activeBlock)?.name }) }}</div>
              <div class="detail-count">{{ t('settings.pipeline.inputCount', { n: activeBlockInputs.length }) }}</div>

              <section v-if="activeBlockInputs.length > 0" class="form-block template-input-list">
                <div v-for="[name, input] in activeBlockInputs" :key="name" class="template-input-row">
                  <label class="field-label" :for="`template-input-${activeBlock.id}-${name}`">
                    {{ input.label || name }}<span v-if="input.required" class="required">*</span>
                    <span v-if="input.description" class="help-icon" :title="input.description" :data-test="`block-${activeBlock.id}-help-${name}`">?</span>
                  </label>

                  <select
                    v-if="input.type === 'select'"
                    :id="`template-input-${activeBlock.id}-${name}`"
                    :value="stringVar(activeBlock, name)"
                    class="settings-select field-input"
                    :data-test="`block-${activeBlock.id}-input-${name}`"
                    @change="setStringVar(activeBlock, name, ($event.target as HTMLSelectElement).value)"
                  >
                    <option v-for="option in input.options ?? []" :key="option" :value="option">{{ option }}</option>
                  </select>

                  <div v-else-if="input.type === 'file_list'" class="file-list">
                    <div class="file-table-head"><span>from</span><span>to</span><span></span></div>
                    <div v-for="(file, index) in fileItems(activeBlock, name)" :key="index" class="file-row">
                      <input
                        class="settings-input field-input file-input"
                        type="text"
                        placeholder="from"
                        :data-test="`block-${activeBlock.id}-file-from-${index}`"
                        :value="file.from"
                        @input="updateFileItem(activeBlock, name, index, 'from', ($event.target as HTMLInputElement).value)"
                      />
                      <input
                        class="settings-input field-input file-input"
                        type="text"
                        placeholder="to"
                        :data-test="`block-${activeBlock.id}-file-to-${index}`"
                        :value="file.to"
                        @input="updateFileItem(activeBlock, name, index, 'to', ($event.target as HTMLInputElement).value)"
                      />
                      <button
                        type="button"
                        class="settings-btn settings-btn-danger danger-btn file-remove"
                        :data-test="`block-${activeBlock.id}-remove-file-${index}`"
                        @click="removeFileItem(activeBlock, name, index)"
                      >
                        <Icon icon="lucide:trash-2" aria-hidden="true" />
                      </button>
                    </div>
                    <button type="button" class="settings-btn settings-btn-text text-btn add-file-btn" :data-test="`block-${activeBlock.id}-add-file`" @click="addFileItem(activeBlock, name)">
                      <Icon icon="lucide:plus" aria-hidden="true" />
                      {{ t('settings.pipeline.addFile') }}
                    </button>
                  </div>

                  <label v-else-if="isBooleanInput(input)" class="boolean-field">
                    <input
                      :id="`template-input-${activeBlock.id}-${name}`"
                      type="checkbox"
                      :checked="boolVar(activeBlock, name)"
                      :data-test="`block-${activeBlock.id}-input-${name}`"
                      @change="setBoolVar(activeBlock, name, ($event.target as HTMLInputElement).checked)"
                    />
                    <span>{{ boolVar(activeBlock, name) ? t('settings.pipeline.booleanTrue') : t('settings.pipeline.booleanFalse') }}</span>
                  </label>

                  <input
                    v-else
                    :id="`template-input-${activeBlock.id}-${name}`"
                    :value="stringVar(activeBlock, name)"
                    class="settings-input field-input"
                    :type="input.type === 'number' ? 'number' : 'text'"
                    :data-test="`block-${activeBlock.id}-input-${name}`"
                    @input="setStringVar(activeBlock, name, ($event.target as HTMLInputElement).value)"
                  />
                </div>
              </section>
            </template>
            <div v-else class="detail-empty">
              {{ t('settings.pipeline.selectTemplate') }}
            </div>
          </aside>
        </div>

        <button
          v-if="!hidePreviewStrip"
          type="button"
          class="settings-btn settings-btn-primary pipeline-save"
          data-test="pipeline-save-template"
          :disabled="!canSave"
          @click="saveTemplate"
        >
          {{ t('settings.pipeline.saveTemplate') }}
        </button>

        <section v-if="!hidePreviewStrip" class="wizard-preview-strip" data-test="wizard-preview-strip">
          <header class="preview-strip-head">
            <span>{{ t('settings.pipeline.preview') }} / PipelinePreview</span>
            <small>{{ t('settings.pipeline.unsavedPreviewHint') }}</small>
          </header>
          <div class="preview-flow">
            <article v-for="block in previewBlocks" :key="block.id" class="preview-node">
              <span class="preview-node-icon" aria-hidden="true"><Icon :icon="block.icon" /></span>
              <strong>{{ block.name }}</strong>
              <small>pending</small>
              <em>{{ block.target }}</em>
            </article>
            <div v-if="previewBlocks.length === 0" class="preview-empty">
              {{ t('settings.pipeline.noTemplate') }}
            </div>
          </div>
        </section>

        <div v-if="previewError" class="preview-error">{{ previewError }}</div>
        <PipelinePreview v-if="preview" :preview="preview" />
    </template>
  </div>
</template>

<style scoped>
.pipeline-wizard {
  display: grid;
  grid-template-columns: minmax(360px, 1fr) minmax(560px, 2fr);
  grid-template-rows: 54px minmax(0, 1fr) auto auto;
  min-width: 0;
  min-height: 0;
  height: 100%;
  background: #0e151d;
}
.pipeline-enable {
  font-size: 11px;
}
.pipeline-save {
  grid-column: 1;
  grid-row: 3;
  justify-self: start;
  margin: 10px 0 0 12px;
}
.wizard-head,
.phase-head,
.block-row {
  display: flex;
  align-items: center;
}
.wizard-head,
.phase-head {
  justify-content: space-between;
  font-size: 13px;
  color: var(--text-secondary);
}
.wizard-head {
  display: none;
}
.phase-tabs {
  grid-column: 1 / -1;
  grid-row: 1;
  display: flex;
  gap: 0;
  height: 54px;
  border: 0;
  border-bottom: 1px solid #263240;
  border-radius: 0;
  margin: 0;
  padding: 10px;
  max-width: none;
  overflow-x: auto;
  overflow-y: hidden;
  background: #0e151d;
  scrollbar-width: thin;
}
.phase-tabs button {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  min-width: 150px;
  height: 34px;
  padding: 0 16px;
  border: 1px solid #263240;
  border-radius: 0;
  background: #0e151d;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}
.phase-tabs button:first-child {
  border-radius: 5px 0 0 5px;
}
.phase-tabs button:last-child {
  border-radius: 0 5px 5px 0;
}
.phase-tabs button + button {
  border-left: 0;
}
.phase-tabs button.active {
  background: linear-gradient(180deg, #2587ff, #176de9);
  border-color: transparent;
  color: #fff;
}
.station-token {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.14);
  color: inherit;
  font-size: 11px;
  font-weight: 600;
}
.wizard-layout {
  display: contents;
  gap: 0;
  align-items: stretch;
  min-height: 520px;
}
.wizard-main {
  grid-column: 1;
  grid-row: 2;
  min-width: 0;
  overflow: auto;
  padding: 0;
  border-top: 0;
  border-right: 1px solid #263240;
  background: #0e151d;
  scrollbar-color: rgba(139, 148, 158, 0.38) rgba(13, 18, 26, 0.72);
}
.wizard-detail-panel {
  grid-column: 2;
  grid-row: 2;
  min-width: 0;
  max-height: 100%;
  overflow: auto;
  border-left: 0;
  padding: 26px 28px 54px;
  background: #121922;
  font-size: 13px;
  scrollbar-color: rgba(139, 148, 158, 0.38) rgba(13, 18, 26, 0.72);
}
.detail-title {
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 650;
}
.detail-subtitle {
  margin-top: 5px;
  color: var(--text-tertiary);
  font-size: 12px;
  line-height: 1.4;
}
.template-block .block-name,
.detail-subtitle {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.detail-count {
  margin-top: 6px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.detail-empty {
  min-height: 180px;
  color: var(--text-tertiary);
  font-size: 12px;
}
.phase-section {
  border-top: 0;
  border-bottom: 1px solid #263240;
  padding: 16px 10px 10px;
  background: transparent;
}
.phase-section:first-of-type {
  border-top: 0;
}
.phase-head {
  display: block;
  min-height: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}
.phase-head small {
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 500;
}
.phase-head .text-btn {
  float: right;
  color: #1f7bff;
}
.pipeline-disable,
.danger-btn,
.text-btn {
  font-size: 11px;
}
.pipeline-empty,
.phase-empty,
.template-description,
.field-help {
  font-size: 11px;
  color: var(--text-tertiary);
}
.pipeline-empty {
  margin: 0 0 10px;
  border: 1px dashed var(--border-secondary);
  border-radius: 6px;
  padding: 10px;
}
.phase-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 48px;
  border: 1px dashed var(--border-secondary);
  border-radius: 6px;
  padding: 8px;
  color: var(--accent);
}
.preview-error {
  grid-column: 1 / -1;
  margin-top: 8px;
  font-size: 11px;
  color: var(--status-failed);
}
.pipeline-wizard :deep(.pipeline-preview) {
  grid-column: 1 / -1;
}
.template-block {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 10px 12px;
  margin-top: 12px;
  background: #0d131b;
  cursor: pointer;
}
.template-block.active {
  border-color: #1f7bff;
  background: rgba(31, 123, 255, 0.16);
  box-shadow: inset 0 0 0 1px rgba(31, 123, 255, 0.26);
}
.template-block.dragging {
  opacity: 0.64;
}
.block-row {
  display: grid;
  grid-template-columns: 18px 24px auto minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 6px 8px;
}
.block-grip {
  grid-column: 1;
  grid-row: 1 / span 2;
  color: var(--text-tertiary);
  font-size: 16px;
  line-height: 0.8;
}
.block-order {
  grid-column: 3;
  grid-row: 1;
  justify-self: center;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 600;
}
.block-cube,
.preview-node-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 2px solid var(--text-tertiary);
  border-radius: 5px;
}
.block-cube {
  grid-column: 2;
  grid-row: 1;
  border-color: #9fb4d2;
  color: #9fb4d2;
}
.block-cube svg,
.preview-node-icon svg {
  width: 13px;
  height: 13px;
}
.block-row .field-input {
  grid-column: 4;
  grid-row: 1;
  min-width: 0;
  height: 28px;
  padding: 0;
  border-color: transparent;
  background: transparent;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}
.block-row .text-btn {
  grid-column: 5;
  grid-row: 1;
  justify-self: start;
}
.block-row .danger-btn {
  grid-column: 6;
  grid-row: 1;
  justify-self: end;
}
.field-label {
  display: block;
  font-size: 11px;
  color: var(--text-secondary);
  margin: 0;
  font-weight: 500;
}
.form-block {
  margin-bottom: 10px;
}
.form-block h4 {
  margin: 0 0 6px;
  color: #f3f6fb;
  font-size: 12px;
  font-weight: 600;
}
.template-input-row {
  display: grid;
  grid-template-columns: 1fr;
  gap: 5px;
  align-items: start;
  margin-top: 6px;
}
.template-input-list {
  display: grid;
  gap: 14px;
}
.required {
  margin-left: 2px;
  color: var(--status-failed);
}
.help-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 5px;
  border-radius: 50%;
  border: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 10px;
}
.file-list {
  display: flex;
  flex-direction: column;
  gap: 0;
  overflow: hidden;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: #121922;
}
.file-table-head {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 42px;
  min-height: 26px;
  color: var(--text-tertiary);
  font-size: 12px;
}
.file-table-head span {
  display: flex;
  align-items: center;
  border-right: 1px solid var(--border-secondary);
  padding: 0 10px;
}
.file-table-head span:last-child {
  border-right: 0;
}
.file-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 42px;
  gap: 0;
  align-items: center;
  border-top: 1px solid var(--border-secondary);
}
.file-row:first-child {
  border-top: 0;
}
.file-input {
  min-width: 0;
  height: 26px;
  border: 0;
  border-right: 1px solid var(--border-secondary);
  border-radius: 0;
}
.file-remove {
  min-height: 26px;
  border: 0;
  border-radius: 0;
  white-space: nowrap;
}
.file-remove svg {
  width: 14px;
  height: 14px;
}
.add-file-btn {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  color: #1f7bff;
}
.boolean-field {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--text-secondary);
  font-size: 12px;
}
.wizard-preview-strip {
  grid-column: 1 / -1;
  grid-row: 4;
  border-top: 1px solid var(--border-secondary);
  padding: 16px 24px;
  background: rgba(21, 30, 42, 0.64);
}
.preview-strip-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 600;
}
.preview-strip-head small {
  color: var(--text-tertiary);
  font-weight: 600;
}
.preview-flow {
  display: grid;
  grid-template-columns: repeat(5, minmax(120px, 1fr));
  gap: 24px;
}
.preview-node {
  position: relative;
  display: grid;
  gap: 4px;
  min-height: 74px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 10px 12px 10px 42px;
  background: rgba(13, 18, 26, 0.72);
}
.preview-node + .preview-node::before {
  position: absolute;
  top: 50%;
  left: -18px;
  color: var(--text-tertiary);
  content: '→';
  transform: translateY(-50%);
}
.preview-node-icon {
  position: absolute;
  top: 14px;
  left: 12px;
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 16%, transparent);
  color: var(--accent);
}
.preview-node strong {
  color: var(--text-primary);
  font-size: 12px;
}
.preview-node small {
  color: var(--text-tertiary);
  font-size: 11px;
}
.preview-node em {
  justify-self: start;
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  padding: 1px 5px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-style: normal;
}
.preview-empty {
  border: 1px dashed var(--border-secondary);
  border-radius: 6px;
  padding: 18px;
  color: var(--text-tertiary);
  font-size: 12px;
}
@media (max-width: 960px) {
  .pipeline-wizard {
    grid-template-columns: 1fr;
  }
  .phase-tabs,
  .wizard-layout,
  .wizard-main,
  .wizard-detail-panel,
  .pipeline-save,
  .wizard-preview-strip,
  .preview-error,
  .pipeline-wizard :deep(.pipeline-preview) {
    grid-column: 1;
    grid-row: auto;
  }
  .wizard-layout {
    display: grid;
    grid-template-columns: 1fr;
    min-height: auto;
    border-top: 1px solid var(--border-secondary);
  }
  .wizard-main {
    max-height: none;
    border-top: 0;
  }
  .wizard-detail-panel {
    max-height: none;
    border-top: 1px solid var(--border-secondary);
    border-left: 0;
  }
  .preview-flow {
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  }
  .preview-node + .preview-node::before {
    display: none;
  }
}
@media (max-width: 780px) {
  .wizard-layout {
    grid-template-columns: 1fr;
  }
  .wizard-detail-panel {
    position: static;
    border-left: 0;
    border-top: 1px solid var(--border-secondary);
  }
  .template-input-row {
    display: grid;
    grid-template-columns: 1fr;
  }
  .block-row {
    grid-template-columns: 18px minmax(0, 1fr) auto;
  }
  .block-grip {
    display: none;
  }
  .block-cube {
    grid-column: 1;
    grid-row: 1;
  }
  .block-order {
    display: none;
  }
  .block-row .field-input {
    grid-column: 2 / 4;
    grid-row: 1;
  }
  .block-row .text-btn {
    grid-column: 2;
    grid-row: 2;
  }
  .block-row .danger-btn {
    grid-column: 3;
    grid-row: 2;
  }
  .file-row {
    grid-template-columns: 1fr;
  }
  .preview-flow {
    grid-template-columns: 1fr;
  }
  .preview-node + .preview-node::before {
    display: none;
  }
}
</style>
