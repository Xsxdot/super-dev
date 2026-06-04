<!--
PipelineTemplateWizard：模板化流水线组合编辑器。

职责：
  - 无 pipeline 时提供「配置流水线」入口
  - 按 build/deploy/finally 阶段组合多个模板 include
  - 把 target_role 输入保存为 pipeline.roles 机器映射
  - 展示后端展开后的流水线预览

边界：
  - 不解析模板 YAML
  - 不直接调用模板/预览 API
  - 不编辑展开后的具体 DAG 步骤
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type {
  Pipeline,
  PipelinePhase,
  PipelinePreviewResponse,
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
  targets: Record<string, string[]>
  runnerTargets: string[]
}

const props = defineProps<{
  modelValue?: Pipeline
  templates: PipelineTemplateSummary[]
  hosts?: Array<{ id: string; name: string }>
  preview?: PipelinePreviewResponse
  previewError?: string
  onViewTemplate?: (template: PipelineTemplateSummary, apply: () => void) => void
  initialMode?: 'template' | 'blank'
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
const nextBlockId = ref(0)
const { t } = useAppI18n()

const canSave = computed(() => blocks.value.length > 0 && blocks.value.every(block => {
  const template = selectedFor(block)
  if (!template) return false
  return Object.entries(template.inputs ?? {}).every(([name, input]) => inputSatisfied(block, name, input))
}))

watch(() => props.modelValue, (value) => {
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

function inputEntries(block: TemplateBlock): [string, TemplateInput][] {
  return Object.entries(selectedFor(block)?.inputs ?? {})
}

function blocksForPhase(phase: PipelinePhase) {
  return blocks.value.filter(block => block.phase === phase)
}

function phaseLabel(phase: PipelinePhase) {
  return t(`settings.pipeline.phases.${phase}`)
}

function addBlock(phase: PipelinePhase) {
  blocks.value.push({ id: String(nextBlockId.value++), phase, selectedKey: '', vars: {}, targets: {}, runnerTargets: [] })
}

function removeBlock(block: TemplateBlock) {
  blocks.value = blocks.value.filter(item => item !== block)
}

function resetBlockInputs(block: TemplateBlock) {
  const template = selectedFor(block)
  block.vars = {}
  block.targets = {}
  for (const [name, input] of Object.entries(template?.inputs ?? {})) {
    if (input.type === 'target_role') {
      block.targets[name] = []
      continue
    }
    if (input.type === 'file_list') {
      block.vars[name] = []
      continue
    }
    block.vars[name] = input.default ?? ''
  }
}

function runnerRoleKey(block: TemplateBlock) {
  return `${block.phase}_${block.id}_runner`
}

function roleKey(block: TemplateBlock, inputName: string) {
  if (inputName === 'role') return `${block.phase}_${block.id}_targets`
  return `${block.phase}_${block.id}_${inputName}_targets`
}

function hydrateFromPipeline(pipeline?: Pipeline) {
  blocks.value = []
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
        targets: {},
        runnerTargets: [],
      }
      for (const role of step.roles ?? []) {
        block.runnerTargets.push(...(pipeline.roles?.[role] ?? []))
      }
      block.runnerTargets = Array.from(new Set(block.runnerTargets))
      for (const [name, value] of Object.entries(vars)) {
        if (typeof value !== 'string') continue
        const ids = pipeline.roles?.[String(value)]
        if (ids) block.targets[name] = [...ids]
      }
      blocks.value.push(block)
    }
  }
}

function enable() {
  enabled.value = true
}

function disable() {
  enabled.value = false
  blocks.value = []
  emit('update:modelValue', undefined)
}

function isTargetChecked(block: TemplateBlock, name: string, hostID: string) {
  return (block.targets[name] ?? []).includes(hostID)
}

function isRunnerChecked(block: TemplateBlock, hostID: string) {
  return block.runnerTargets.includes(hostID)
}

function toggleRunner(block: TemplateBlock, hostID: string, checked: boolean) {
  const set = new Set(block.runnerTargets)
  if (checked) set.add(hostID)
  else set.delete(hostID)
  block.runnerTargets = [...set]
}

function toggleTarget(block: TemplateBlock, name: string, hostID: string, checked: boolean) {
  const set = new Set(block.targets[name] ?? [])
  if (checked) set.add(hostID)
  else set.delete(hostID)
  block.targets[name] = [...set]
}

function inputSatisfied(block: TemplateBlock, name: string, input: TemplateInput) {
  if (input.type === 'target_role') return !input.required || (block.targets[name] ?? []).length > 0
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
  const pipeline: Pipeline = { build: [], deploy: [], finally: [], roles: {}, variables: {} }
  for (const block of blocks.value) {
    const template = selectedFor(block)
    if (!template) continue
    const vars: Record<string, TemplateVarValue> = { ...block.vars }
    for (const [name, input] of Object.entries(template.inputs ?? {})) {
      if (input.type === 'target_role') {
        const key = roleKey(block, name)
        vars[name] = key
        pipeline.roles![key] = block.targets[name] ?? []
      }
      if (input.type === 'file_list' && fileItems(block, name).length === 0 && !input.required) {
        delete vars[name]
      }
    }
    if (typeof vars.app_name === 'string' && vars.app_name && !pipeline.variables!.app_name) {
      pipeline.variables!.app_name = vars.app_name
    }
    const runnerTargets = block.runnerTargets.filter(Boolean)
    const runnerKey = runnerRoleKey(block)
    if (runnerTargets.length > 0) {
      pipeline.roles![runnerKey] = runnerTargets
    }
    pipeline[block.phase]!.push({
      name: template.name,
      type: 'include',
      roles: runnerTargets.length > 0 ? [runnerKey] : undefined,
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
</script>

<template>
  <div class="pipeline-wizard">
    <button v-if="!enabled" type="button" class="pipeline-enable" data-test="pipeline-enable" @click="enable">
      {{ t('settings.pipeline.configure') }}
    </button>

    <template v-else>
      <div class="wizard-head">
        <span>{{ t('settings.pipeline.template') }}</span>
        <button type="button" class="pipeline-disable" @click="disable">{{ t('settings.pipeline.removeTemplate') }}</button>
      </div>

      <div v-if="templates.length === 0" class="pipeline-empty">
        {{ t('settings.pipeline.emptyTemplates') }}
      </div>

      <template v-else>
        <section v-for="phase in phases" :key="phase" class="phase-section">
          <header class="phase-head">
            <span>{{ phaseLabel(phase) }}</span>
            <button type="button" class="text-btn" :data-test="`add-template-${phase}`" @click="addBlock(phase)">
              {{ t('settings.pipeline.addTemplate') }}
            </button>
          </header>

          <div v-if="blocksForPhase(phase).length === 0" class="phase-empty">{{ t('settings.pipeline.noTemplate') }}</div>

          <div v-for="block in blocksForPhase(phase)" :key="block.id" class="template-block">
            <div class="block-row">
              <select
                v-model="block.selectedKey"
                class="field-input"
                :data-test="`block-${block.id}-template-select`"
                @change="resetBlockInputs(block)"
              >
                <option value="" disabled>{{ t('settings.pipeline.selectTemplate') }}</option>
                <option v-for="template in templatesForBlock(block)" :key="templateKey(template)" :value="templateKey(template)">
                  {{ template.name }} · {{ template.source }} · {{ template.version }}
                </option>
              </select>
              <button
                type="button"
                class="text-btn"
                :data-test="`block-${block.id}-view-template`"
                :disabled="!selectedFor(block)"
                @click="viewSelected(block)"
              >
                {{ t('settings.pipeline.viewTemplate') }}
              </button>
              <button type="button" class="danger-btn" @click="removeBlock(block)">{{ t('common.remove') }}</button>
            </div>

            <div v-if="selectedFor(block)?.description" class="template-description">
              {{ selectedFor(block)?.description }}
            </div>

            <div class="template-runner-row">
              <div class="field-label">{{ t('settings.pipeline.machine') }}</div>
              <div class="field-help">{{ t('settings.pipeline.machineHelp') }}</div>
              <div v-if="(hosts ?? []).length === 0" class="field-help">{{ t('settings.pipeline.noHostsHelp') }}</div>
              <div v-else class="target-list target-grid" :data-test="`block-${block.id}-runner-targets`">
                <label v-for="host in hosts ?? []" :key="host.id" class="target-item">
                  <input
                    type="checkbox"
                    :data-test="`block-${block.id}-runner-${host.id}`"
                    :checked="isRunnerChecked(block, host.id)"
                    @change="toggleRunner(block, host.id, ($event.target as HTMLInputElement).checked)"
                  />
                  {{ host.name }}
                </label>
              </div>
            </div>

            <div v-for="[name, input] in inputEntries(block)" :key="name" class="template-input-row">
              <label class="field-label" :for="`template-input-${block.id}-${name}`">
                {{ input.label || name }}<span v-if="input.required" class="required">*</span>
                <span v-if="input.description" class="help-icon" :title="input.description" :data-test="`block-${block.id}-help-${name}`">?</span>
              </label>

              <div v-if="input.type === 'target_role'" class="target-list target-grid" :data-test="`block-${block.id}-${name}-targets`">
                <label v-for="host in hosts ?? []" :key="host.id" class="target-item">
                  <input
                    type="checkbox"
                    :data-test="`block-${block.id}-target-${host.id}`"
                    :checked="isTargetChecked(block, name, host.id)"
                    @change="toggleTarget(block, name, host.id, ($event.target as HTMLInputElement).checked)"
                  />
                  {{ host.name }}
                </label>
                <div v-if="(hosts ?? []).length === 0" class="field-help">{{ t('settings.pipeline.noHostsHelp') }}</div>
              </div>

              <select
                v-else-if="input.type === 'select'"
                :id="`template-input-${block.id}-${name}`"
                :value="stringVar(block, name)"
                class="field-input"
                :data-test="`block-${block.id}-input-${name}`"
                @change="setStringVar(block, name, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="option in input.options ?? []" :key="option" :value="option">{{ option }}</option>
              </select>

              <div v-else-if="input.type === 'file_list'" class="file-list">
                <div v-for="(file, index) in fileItems(block, name)" :key="index" class="file-row">
                  <input
                    class="field-input file-input"
                    type="text"
                    placeholder="from"
                    :data-test="`block-${block.id}-file-from-${index}`"
                    :value="file.from"
                    @input="updateFileItem(block, name, index, 'from', ($event.target as HTMLInputElement).value)"
                  />
                  <input
                    class="field-input file-input"
                    type="text"
                    placeholder="to"
                    :data-test="`block-${block.id}-file-to-${index}`"
                    :value="file.to"
                    @input="updateFileItem(block, name, index, 'to', ($event.target as HTMLInputElement).value)"
                  />
                  <button
                    type="button"
                    class="danger-btn file-remove"
                    :data-test="`block-${block.id}-remove-file-${index}`"
                    @click="removeFileItem(block, name, index)"
                  >
                    {{ t('common.remove') }}
                  </button>
                </div>
                <button type="button" class="text-btn" :data-test="`block-${block.id}-add-file`" @click="addFileItem(block, name)">
                  {{ t('settings.pipeline.addFile') }}
                </button>
              </div>

              <input
                v-else
                :id="`template-input-${block.id}-${name}`"
                :value="stringVar(block, name)"
                class="field-input"
                :type="input.type === 'number' ? 'number' : 'text'"
                :data-test="`block-${block.id}-input-${name}`"
                @input="setStringVar(block, name, ($event.target as HTMLInputElement).value)"
              />
            </div>
          </div>
        </section>

        <button type="button" class="pipeline-save" data-test="pipeline-save-template" :disabled="!canSave" @click="saveTemplate">
          {{ t('settings.pipeline.saveTemplate') }}
        </button>

        <div v-if="previewError" class="preview-error">{{ previewError }}</div>
        <PipelinePreview v-if="preview" :preview="preview" />
      </template>
    </template>
  </div>
</template>

<style scoped>
.pipeline-enable,
.pipeline-save {
  padding: 3px 8px;
  font-size: 11px;
  background: var(--bg-overlay);
  border: 1px solid var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
}
.pipeline-save {
  margin-top: 8px;
}
.pipeline-save:disabled,
.text-btn:disabled {
  cursor: not-allowed;
  opacity: 0.5;
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
  font-size: 11px;
  color: var(--text-secondary);
}
.wizard-head {
  margin-bottom: 8px;
}
.phase-section {
  border-top: 1px solid var(--border-secondary);
  padding-top: 8px;
  margin-top: 8px;
}
.pipeline-disable,
.danger-btn {
  background: transparent;
  border: none;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
.text-btn {
  background: transparent;
  border: none;
  color: var(--accent);
  cursor: pointer;
  font-size: 11px;
  padding: 0;
}
.pipeline-empty,
.phase-empty,
.template-description,
.field-help {
  font-size: 11px;
  color: var(--text-tertiary);
}
.phase-empty {
  padding: 5px 0;
}
.preview-error {
  margin-top: 8px;
  font-size: 11px;
  color: var(--status-failed);
}
.template-block {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 8px;
  margin-top: 6px;
}
.block-row {
  gap: 8px;
}
.field-label {
  display: block;
  font-size: 11px;
  color: var(--text-secondary);
  margin: 6px 0 2px;
  font-weight: 600;
}
.field-input {
  display: block;
  width: 100%;
  padding: 4px 8px;
  font-size: 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  color: var(--text-primary);
  outline: none;
  box-sizing: border-box;
}
.template-input-row {
  margin-bottom: 6px;
}
.template-runner-row {
  margin: 6px 0;
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
.target-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 6px 12px;
  align-items: center;
}
.target-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  font-size: 12px;
  color: var(--text-secondary);
}
.file-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.file-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  gap: 6px;
  align-items: center;
}
.file-input {
  min-width: 0;
}
.file-remove {
  white-space: nowrap;
}
</style>
