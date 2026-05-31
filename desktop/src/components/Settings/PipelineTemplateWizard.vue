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
import type { Pipeline, PipelinePhase, PipelinePreviewResponse, PipelineTemplateSummary, TemplateInput } from '@/api/agent'
import PipelinePreview from './PipelinePreview.vue'

type TemplateBlock = {
  id: string
  phase: PipelinePhase
  selectedKey: string
  vars: Record<string, string>
  targets: Record<string, string[]>
}

const props = defineProps<{
  modelValue?: Pipeline
  templates: PipelineTemplateSummary[]
  hosts?: Array<{ id: string; name: string }>
  preview?: PipelinePreviewResponse
  previewError?: string
  onViewTemplate?: (template: PipelineTemplateSummary) => void
}>()
const emit = defineEmits<{ 'update:modelValue': [Pipeline | undefined] }>()

const phases: PipelinePhase[] = ['build', 'deploy', 'finally']
const phaseLabels: Record<PipelinePhase, string> = {
  build: '构建阶段',
  deploy: '部署阶段',
  finally: '清理阶段',
}
const enabled = ref(Boolean(props.modelValue))
const blocks = ref<TemplateBlock[]>([])
const nextBlockId = ref(0)

const canSave = computed(() => blocks.value.length > 0 && blocks.value.every(block => {
  const template = selectedFor(block)
  if (!template) return false
  return Object.entries(template.inputs ?? {}).every(([name, input]) => {
    if (!input.required) return true
    if (input.type === 'target_role') return (block.targets[name] ?? []).length > 0
    return (block.vars[name] ?? '').trim() !== ''
  })
}))

watch(() => props.modelValue, (value) => {
  enabled.value = Boolean(value) || enabled.value
  hydrateFromPipeline(value)
}, { immediate: true })

function templateKey(template: PipelineTemplateSummary) {
  return `${template.source}://${template.id}@${template.version}`
}

function selectedFor(block: TemplateBlock) {
  return props.templates.find(t => templateKey(t) === block.selectedKey)
}

function inputEntries(block: TemplateBlock): [string, TemplateInput][] {
  return Object.entries(selectedFor(block)?.inputs ?? {})
}

function blocksForPhase(phase: PipelinePhase) {
  return blocks.value.filter(block => block.phase === phase)
}

function addBlock(phase: PipelinePhase) {
  blocks.value.push({ id: String(nextBlockId.value++), phase, selectedKey: '', vars: {}, targets: {} })
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
    block.vars[name] = input.default ?? ''
  }
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
      const vars = Object.fromEntries(Object.entries(rawVars).map(([key, value]) => [key, String(value)]))
      const block: TemplateBlock = {
        id: String(nextBlockId.value++),
        phase,
        selectedKey: version ? `${templateURI}@${version}` : '',
        vars,
        targets: {},
      }
      for (const [name, value] of Object.entries(vars)) {
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

function toggleTarget(block: TemplateBlock, name: string, hostID: string, checked: boolean) {
  const set = new Set(block.targets[name] ?? [])
  if (checked) set.add(hostID)
  else set.delete(hostID)
  block.targets[name] = [...set]
}

function viewSelected(block: TemplateBlock) {
  const template = selectedFor(block)
  if (template) props.onViewTemplate?.(template)
}

function saveTemplate() {
  const pipeline: Pipeline = { build: [], deploy: [], finally: [], roles: {}, variables: {} }
  for (const block of blocks.value) {
    const template = selectedFor(block)
    if (!template) continue
    const vars: Record<string, string> = { ...block.vars }
    for (const [name, input] of Object.entries(template.inputs ?? {})) {
      if (input.type !== 'target_role') continue
      const key = roleKey(block, name)
      vars[name] = key
      pipeline.roles![key] = block.targets[name] ?? []
    }
    if (vars.app_name && !pipeline.variables!.app_name) {
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
</script>

<template>
  <div class="pipeline-wizard">
    <button v-if="!enabled" type="button" class="pipeline-enable" data-test="pipeline-enable" @click="enable">
      + 配置流水线
    </button>

    <template v-else>
      <div class="wizard-head">
        <span>流水线模板</span>
        <button type="button" class="pipeline-disable" @click="disable">移除流水线</button>
      </div>

      <div v-if="templates.length === 0" class="pipeline-empty">
        暂无可用模板，请先导入或创建模板。
      </div>

      <template v-else>
        <section v-for="phase in phases" :key="phase" class="phase-section">
          <header class="phase-head">
            <span>{{ phaseLabels[phase] }}</span>
            <button type="button" class="text-btn" :data-test="`add-template-${phase}`" @click="addBlock(phase)">
              + 添加模板
            </button>
          </header>

          <div v-if="blocksForPhase(phase).length === 0" class="phase-empty">未添加模板</div>

          <div v-for="block in blocksForPhase(phase)" :key="block.id" class="template-block">
            <div class="block-row">
              <select
                v-model="block.selectedKey"
                class="field-input"
                :data-test="`block-${block.id}-template-select`"
                @change="resetBlockInputs(block)"
              >
                <option value="" disabled>请选择模板</option>
                <option v-for="template in templates" :key="templateKey(template)" :value="templateKey(template)">
                  {{ template.name }} · {{ template.source }} · {{ template.version }}
                </option>
              </select>
              <button type="button" class="text-btn" :disabled="!selectedFor(block)" @click="viewSelected(block)">查看模板</button>
              <button type="button" class="danger-btn" @click="removeBlock(block)">移除</button>
            </div>

            <div v-if="selectedFor(block)?.description" class="template-description">
              {{ selectedFor(block)?.description }}
            </div>

            <div v-for="[name, input] in inputEntries(block)" :key="name" class="template-input-row">
              <label class="field-label" :for="`template-input-${block.id}-${name}`">
                {{ input.label || name }}<span v-if="input.required" class="required">*</span>
                <span v-if="input.description" class="help-icon" :title="input.description" :data-test="`block-${block.id}-help-${name}`">?</span>
              </label>

              <div v-if="input.type === 'target_role'" class="target-list">
                <label v-for="host in hosts ?? []" :key="host.id" class="target-item">
                  <input
                    type="checkbox"
                    :data-test="`block-${block.id}-target-${host.id}`"
                    :checked="isTargetChecked(block, name, host.id)"
                    @change="toggleTarget(block, name, host.id, ($event.target as HTMLInputElement).checked)"
                  />
                  {{ host.name }}
                </label>
                <div v-if="(hosts ?? []).length === 0" class="field-help">还没有可选主机，请先在主机管理中添加。</div>
              </div>

              <select
                v-else-if="input.type === 'select'"
                :id="`template-input-${block.id}-${name}`"
                v-model="block.vars[name]"
                class="field-input"
                :data-test="`block-${block.id}-input-${name}`"
              >
                <option v-for="option in input.options ?? []" :key="option" :value="option">{{ option }}</option>
              </select>

              <input
                v-else
                :id="`template-input-${block.id}-${name}`"
                v-model="block.vars[name]"
                class="field-input"
                :type="input.type === 'number' ? 'number' : 'text'"
                :data-test="`block-${block.id}-input-${name}`"
              />
            </div>
          </div>
        </section>

        <button type="button" class="pipeline-save" data-test="pipeline-save-template" :disabled="!canSave" @click="saveTemplate">
          保存流水线
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
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.target-item {
  font-size: 12px;
  color: var(--text-secondary);
}
</style>
