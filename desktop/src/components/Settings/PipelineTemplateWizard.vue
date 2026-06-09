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
  targets: Record<string, string[]>
  runnerTargets: string[]
}

type HostOption = { id: string; name: string }
type InputGroupKey = 'machine' | 'path' | 'command' | 'file' | 'optional'
type InputGroupSelection = InputGroupKey | 'all'

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
const activeInputGroup = ref<InputGroupSelection>('all')
const activePhase = ref<PipelinePhase>('build')
const nextBlockId = ref(0)
const { t } = useAppI18n()

const canSave = computed(() => blocks.value.length > 0 && blocks.value.every(block => {
  const template = selectedFor(block)
  if (!template) return false
  return Object.entries(template.inputs ?? {}).every(([name, input]) => inputSatisfied(block, name, input))
}))
const activeBlock = computed(() => blocks.value.find(block => block.id === activeBlockId.value) ?? blocks.value[0] ?? null)
const inputGroups = computed(() => {
  const block = activeBlock.value
  return [
    {
      key: 'machine' as const,
      label: t('settings.pipeline.machine'),
      count: block ? 1 + groupedInputEntries('machine').length : 0,
    },
    { key: 'path' as const, label: t('settings.pipeline.inputGroupPath'), count: groupedInputEntries('path').length },
    { key: 'command' as const, label: t('settings.pipeline.inputGroupCommand'), count: groupedInputEntries('command').length },
    { key: 'file' as const, label: t('settings.pipeline.inputGroupFile'), count: groupedInputEntries('file').length },
    { key: 'optional' as const, label: t('settings.pipeline.inputGroupOptional'), count: groupedInputEntries('optional').length },
  ]
})
const detailInputGroups = computed(() =>
  inputGroups.value
    .filter(group => group.key !== 'machine')
    .map(group => ({ ...group, entries: groupedInputEntries(group.key) }))
    .filter(group => group.entries.length > 0),
)
const previewBlocks = computed(() =>
  blocks.value
    .map(block => ({
      id: block.id,
      phase: block.phase,
      name: selectedFor(block)?.name || phaseLabel(block.phase),
      target: block.runnerTargets[0] || Object.values(block.targets)[0]?.[0] || t('common.local'),
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

function inputEntries(block: TemplateBlock): [string, TemplateInput][] {
  return Object.entries(selectedFor(block)?.inputs ?? {})
}

function inputGroupFor(name: string, input: TemplateInput): InputGroupKey {
  const key = name.toLowerCase()
  if (input.type === 'target_role') return 'machine'
  if (input.type === 'file_list') return 'file'
  if (input.type === 'path' || key.includes('dir') || key.includes('path')) return 'path'
  if (key.includes('command') || key.includes('cmd')) return 'command'
  return 'optional'
}

function groupedInputEntries(group: InputGroupKey): [string, TemplateInput][] {
  const block = activeBlock.value
  if (!block) return []
  return inputEntries(block)
    .filter(([name, input]) => inputGroupFor(name, input) === group)
    .sort(([left], [right]) => inputSortRank(group, left) - inputSortRank(group, right))
}

function inputSortRank(group: InputGroupKey, name: string) {
  const key = name.toLowerCase()
  const ranks: Record<InputGroupKey, string[]> = {
    machine: ['runner', 'target', 'role'],
    path: ['frontend_dir', 'frontend_directory', 'backend_dir', 'backend_directory', 'binary_output', 'artifact_path', 'artifact'],
    command: ['frontend_build_cmd', 'frontend_build_command', 'build_command', 'command'],
    file: ['package_files', 'files'],
    optional: ['go_build_env', 'go_package'],
  }
  const index = ranks[group].findIndex(item => key === item || key.includes(item))
  return index === -1 ? 100 : index
}

function selectInputGroup(group: InputGroupKey) {
  activeInputGroup.value = group
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

function roleHostIDs(pipeline: Pipeline | undefined, role: string) {
  return pipeline?.roles?.[role] ?? props.pipelineRoles?.[role]?.hosts ?? []
}

function phaseIcon(phase: PipelinePhase) {
  if (phase === 'deploy') return 'lucide:server'
  if (phase === 'finally') return 'lucide:shield-check'
  return 'lucide:package'
}

function addBlock(phase: PipelinePhase) {
  const block = { id: String(nextBlockId.value++), phase, selectedKey: '', vars: {}, targets: {}, runnerTargets: [] }
  blocks.value.push(block)
  activeBlockId.value = block.id
  activePhase.value = phase
  activeInputGroup.value = 'all'
}

function removeBlock(block: TemplateBlock) {
  blocks.value = blocks.value.filter(item => item !== block)
  if (activeBlockId.value === block.id) {
    activeBlockId.value = blocks.value[0]?.id ?? null
    activeInputGroup.value = 'all'
  }
}

function resetBlockInputs(block: TemplateBlock) {
  activeInputGroup.value = 'all'
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
  activeBlockId.value = null
  activePhase.value = 'build'
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
      const runnerRoles = step.roles?.length ? step.roles : [runnerRoleKey(block)]
      for (const role of runnerRoles) {
        block.runnerTargets.push(...roleHostIDs(pipeline, role))
      }
      block.runnerTargets = Array.from(new Set(block.runnerTargets))
      for (const [name, value] of Object.entries(vars)) {
        if (typeof value !== 'string') continue
        const ids = roleHostIDs(pipeline, String(value))
        if (ids) block.targets[name] = [...ids]
      }
      blocks.value.push(block)
      if (!activeBlockId.value) {
        activeBlockId.value = block.id
        activePhase.value = phase
      }
    }
  }
}

function enable() {
  enabled.value = true
}

function disable() {
  enabled.value = false
  blocks.value = []
  activePhase.value = 'build'
  emit('update:modelValue', undefined)
}

function hostAliases(host: HostOption) {
  return Array.from(new Set([host.id, host.name].filter(Boolean)))
}

function includesHost(values: string[], host: HostOption) {
  const aliases = hostAliases(host)
  return aliases.some(alias => values.includes(alias))
}

function isTargetHostChecked(block: TemplateBlock, name: string, host: HostOption) {
  return includesHost(block.targets[name] ?? [], host)
}

function isRunnerHostChecked(block: TemplateBlock, host: HostOption) {
  return includesHost(block.runnerTargets, host)
}

function compactRunnerHosts(block: TemplateBlock) {
  const allHosts = props.hosts ?? []
  if (allHosts.length <= 3) return allHosts
  const selected = allHosts.filter(host => isRunnerHostChecked(block, host))
  const byID = new Map<string, HostOption>()
  for (const host of [...selected, ...allHosts]) {
    if (!byID.has(host.id)) byID.set(host.id, host)
    if (byID.size >= 3) break
  }
  return [...byID.values()]
}

function hiddenRunnerHostCount(block: TemplateBlock) {
  return Math.max(0, (props.hosts?.length ?? 0) - compactRunnerHosts(block).length)
}

function updateHostSelection(values: string[], host: HostOption, checked: boolean) {
  const aliases = new Set(hostAliases(host))
  const next = values.filter(value => !aliases.has(value))
  if (checked) next.push(host.id)
  return next
}

function toggleRunnerHost(block: TemplateBlock, host: HostOption, checked: boolean) {
  block.runnerTargets = updateHostSelection(block.runnerTargets, host, checked)
}

function toggleTargetHost(block: TemplateBlock, name: string, host: HostOption, checked: boolean) {
  block.targets[name] = updateHostSelection(block.targets[name] ?? [], host, checked)
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
  activeInputGroup.value = 'all'
}

function selectPhase(phase: PipelinePhase) {
  activePhase.value = phase
  const firstBlock = blocksForPhase(phase)[0]
  if (firstBlock) selectBlock(firstBlock)
}

function blockVarSummary(block: TemplateBlock) {
  return Object.values(block.vars)
    .filter((value): value is string => typeof value === 'string' && value.trim() !== '')
    .slice(0, 4)
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
            v-for="phase in phases"
            :key="phase"
            type="button"
            :class="{ active: activePhase === phase }"
            :data-test="`pipeline-phase-tab-${phase}`"
            @click="selectPhase(phase)"
          >
            {{ phaseDisplayLabel(phase) }}
          </button>
        </div>

        <div class="wizard-layout">
          <div class="wizard-main" data-test="pipeline-wizard-canvas">
            <div v-if="templates.length === 0" class="pipeline-empty">
              {{ t('settings.pipeline.emptyTemplates') }}
            </div>
            <section v-for="phase in phases" :key="phase" class="phase-section">
              <header class="phase-head">
                <span>{{ phaseDisplayLabel(phase) }} <small>{{ t('settings.pipeline.phaseSuffix') }}</small></span>
                <button type="button" class="settings-btn settings-btn-text text-btn" :data-test="`add-template-${phase}`" @click="addBlock(phase)">
                  {{ t('settings.pipeline.addTemplate') }}
                </button>
              </header>

              <div v-if="blocksForPhase(phase).length === 0" class="phase-empty">{{ t('settings.pipeline.noTemplate') }}</div>

              <div
                v-for="block in blocksForPhase(phase)"
                :key="block.id"
                class="template-block"
                :class="{ active: activeBlock?.id === block.id }"
                @click="selectBlock(block)"
              >
                <div class="block-row">
                  <span class="block-grip" aria-hidden="true">⋮⋮</span>
                  <span class="block-cube" aria-hidden="true"><Icon :icon="phaseIcon(block.phase)" /></span>
                  <select
                    v-model="block.selectedKey"
                    class="settings-select field-input"
                    :data-test="`block-${block.id}-template-select`"
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

                <div v-if="selectedFor(block) && activeBlock?.id === block.id" class="block-inline-detail">
                  <div class="block-inline-section">
                    <div class="inline-label">
                      {{ t('settings.pipeline.machine') }} / Runner machines
                      <span class="help-icon" :title="t('settings.pipeline.machineHelp')">?</span>
                    </div>
                    <div class="inline-target-list" :data-test="`block-${block.id}-inline-runner-targets`">
                      <label v-for="host in compactRunnerHosts(block)" :key="host.id" class="target-item">
                        <input
                          type="checkbox"
                          :checked="isRunnerHostChecked(block, host)"
                          @change="toggleRunnerHost(block, host, ($event.target as HTMLInputElement).checked)"
                          @click.stop
                        />
                        {{ host.name }}
                      </label>
                      <span v-if="hiddenRunnerHostCount(block) > 0" class="target-more">+{{ hiddenRunnerHostCount(block) }}</span>
                      <span v-if="(hosts ?? []).length === 0" class="field-help">{{ t('settings.pipeline.noHostsHelp') }}</span>
                    </div>
                  </div>

                  <div class="block-inline-section">
                    <div class="inline-label">
                      {{ t('settings.pipeline.keyVars') }} / Key vars ({{ blockVarSummary(block).length }})
                    </div>
                    <div class="inline-key-vars" :data-test="`block-${block.id}-inline-key-vars`">
                      <span v-for="value in blockVarSummary(block)" :key="value" class="summary-chip">{{ value }}</span>
                    </div>
                  </div>
                </div>

                <div class="block-summary">
                  <span>{{ t('settings.pipeline.machine') }}: {{ block.runnerTargets.length || 0 }}</span>
                  <span v-for="value in blockVarSummary(block)" :key="value" class="summary-chip">{{ value }}</span>
                </div>
              </div>
            </section>
          </div>

          <aside class="wizard-detail-panel" data-test="pipeline-wizard-detail">
            <template v-if="activeBlock && selectedFor(activeBlock)">
              <div class="detail-title">{{ t('settings.pipeline.dynamicInputs') }}</div>
              <div class="detail-subtitle">{{ t('settings.pipeline.currentTemplate', { name: selectedFor(activeBlock)?.name }) }}</div>
              <div class="detail-tabs">
                <button
                  v-for="group in inputGroups"
                  :key="group.key"
                  type="button"
                  :class="{ active: activeInputGroup === group.key || (activeInputGroup === 'all' && group.key === 'machine') }"
                  :data-test="`input-group-${group.key}`"
                  @click="selectInputGroup(group.key)"
                >
                  <span>{{ group.label }}</span>
                  <small>{{ group.count }}</small>
                </button>
              </div>

              <section class="form-block template-runner-row">
                <h4>{{ t('settings.pipeline.machine') }} / Runner machines <span class="help-icon" :title="t('settings.pipeline.machineHelp')">?</span></h4>
                <div v-if="(hosts ?? []).length === 0" class="field-help">{{ t('settings.pipeline.noHostsHelp') }}</div>
                <div v-else class="target-list target-grid" :data-test="`block-${activeBlock.id}-runner-targets`">
                  <label v-for="host in compactRunnerHosts(activeBlock)" :key="host.id" class="target-item">
                    <input
                      type="checkbox"
                      :data-test="`block-${activeBlock.id}-runner-${host.id}`"
                      :checked="isRunnerHostChecked(activeBlock, host)"
                      @change="toggleRunnerHost(activeBlock, host, ($event.target as HTMLInputElement).checked)"
                    />
                    {{ host.name }}
                  </label>
                  <span v-if="hiddenRunnerHostCount(activeBlock) > 0" class="target-more">+{{ hiddenRunnerHostCount(activeBlock) }}</span>
                </div>

                <div v-for="[name, input] in groupedInputEntries('machine')" :key="name" class="template-input-row">
                  <label class="field-label">{{ input.label || name }}<span v-if="input.required" class="required">*</span></label>
                  <div class="target-list target-grid" :data-test="`block-${activeBlock.id}-${name}-targets`">
                    <label v-for="host in hosts ?? []" :key="host.id" class="target-item">
                      <input
                        type="checkbox"
                        :data-test="`block-${activeBlock.id}-target-${host.id}`"
                        :checked="isTargetHostChecked(activeBlock, name, host)"
                        @change="toggleTargetHost(activeBlock, name, host, ($event.target as HTMLInputElement).checked)"
                      />
                      {{ host.name }}
                    </label>
                  </div>
                </div>
              </section>

              <section v-for="group in detailInputGroups" :key="group.key" class="form-block">
                <h4>{{ group.label }}</h4>
                <div v-for="[name, input] in group.entries" :key="name" class="template-input-row">
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
  grid-template-columns: minmax(0, 1fr) 504px;
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
  grid-column: 1;
  grid-row: 1;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  height: 54px;
  border: 0;
  border-bottom: 1px solid #263240;
  border-radius: 0;
  margin: 0;
  padding: 10px;
  max-width: none;
  overflow: hidden;
  background: #0e151d;
}
.phase-tabs button {
  height: 34px;
  border: 1px solid #263240;
  border-radius: 0;
  background: #0e151d;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 13px;
  font-weight: 700;
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
  grid-row: 1 / span 4;
  min-width: 0;
  max-height: 100%;
  overflow: auto;
  border-left: 0;
  padding: 12px 16px 54px;
  background: #121922;
  font-size: 13px;
  scrollbar-color: rgba(139, 148, 158, 0.38) rgba(13, 18, 26, 0.72);
}
.detail-title {
  color: var(--text-primary);
  font-size: 16px;
  font-weight: 800;
}
.detail-subtitle {
  margin-top: 5px;
  color: var(--text-tertiary);
  font-size: 12px;
  line-height: 1.4;
}
.detail-tabs {
  display: grid;
  grid-template-columns: 1.1fr 0.9fr 0.9fr 0.9fr 0.8fr;
  height: 29px;
  margin: 10px 0 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
  background: #111820;
}
.detail-tabs button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  min-width: 0;
  padding: 0 4px;
  border: 0;
  border-left: 1px solid var(--border-secondary);
  background: #111820;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 11px;
  text-align: center;
}
.detail-tabs button:first-child {
  border-left: 0;
}
.detail-tabs .active {
  background: #113d78;
  color: #58a0ff;
  font-weight: 800;
}
.detail-tabs small {
  border-radius: 999px;
  padding: 0 5px;
  background: rgba(139, 148, 158, 0.16);
  color: inherit;
  font-size: 10px;
  line-height: 16px;
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
  font-weight: 800;
}
.phase-head small {
  color: var(--text-tertiary);
  font-size: 14px;
  font-weight: 700;
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
.block-inline-detail {
  display: grid;
  gap: 12px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border-secondary);
}
.block-inline-section {
  display: grid;
  gap: 8px;
}
.inline-label {
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.inline-target-list,
.inline-key-vars {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  align-items: center;
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
  padding: 12px;
  margin-top: 12px;
  background: #0d131b;
  cursor: pointer;
}
.template-block.active {
  border-color: #1f7bff;
  box-shadow: inset 0 0 0 1px rgba(31, 123, 255, 0.22);
}
.block-row {
  gap: 8px;
}
.block-grip {
  color: var(--text-tertiary);
  font-size: 16px;
  line-height: 0.8;
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
  border-color: #9fb4d2;
  color: #9fb4d2;
}
.block-cube svg,
.preview-node-icon svg {
  width: 13px;
  height: 13px;
}
.block-row .field-input {
  flex: 1;
  min-width: 0;
  border-color: transparent;
  background: transparent;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 800;
}
.field-label {
  display: block;
  font-size: 13px;
  color: var(--text-secondary);
  margin: 0;
  font-weight: 600;
}
.form-block {
  margin-bottom: 10px;
}
.form-block h4 {
  margin: 0 0 6px;
  color: #f3f6fb;
  font-size: 14px;
  font-weight: 800;
}
.template-input-row {
  display: grid;
  grid-template-columns: 1fr;
  gap: 5px;
  align-items: start;
  margin-top: 6px;
}
.template-runner-row {
  display: block;
  margin-top: 14px;
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
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px 12px;
  align-items: center;
  margin-top: 8px;
}
.target-item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  font-size: 12px;
  color: var(--text-primary);
}
.target-more {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: fit-content;
  min-width: 30px;
  height: 20px;
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 800;
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
.block-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 9px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.template-block.active .block-summary {
  display: none;
}
.summary-chip {
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  padding: 5px 7px;
  background: #101821;
  color: #f5f8fd;
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
  font-weight: 800;
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
@media (max-width: 1280px) {
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
  .detail-tabs {
    grid-template-columns: repeat(5, minmax(74px, 1fr));
    overflow-x: auto;
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
  .block-row,
  .template-input-row,
  .template-runner-row {
    display: grid;
    grid-template-columns: 1fr;
  }
  .template-runner-row .field-help,
  .template-runner-row .target-list {
    grid-column: auto;
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
