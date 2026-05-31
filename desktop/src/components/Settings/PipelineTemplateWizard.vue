<!--
PipelineTemplateWizard：模板化流水线配置向导。

职责：
  - 无 pipeline 时提供「配置流水线」入口
  - 选择已发布模板并填写模板 inputs
  - 保存时生成 include step，交由后端预览和展开

边界：
  - 不解析模板 YAML
  - 不直接调用模板/预览 API
  - 不编辑展开后的具体 DAG 步骤
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { Pipeline, PipelinePreviewResponse, PipelineTemplateSummary, TemplateInput } from '@/api/agent'
import PipelinePreview from './PipelinePreview.vue'

const props = defineProps<{
  modelValue?: Pipeline
  templates: PipelineTemplateSummary[]
  preview?: PipelinePreviewResponse
  previewError?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [Pipeline | undefined] }>()

const enabled = ref(Boolean(props.modelValue))
const selectedKey = ref('')
const vars = reactive<Record<string, string>>({})
let hydratingPipeline = false

const selected = computed(() => props.templates.find(t => templateKey(t) === selectedKey.value))
const inputEntries = computed<[string, TemplateInput][]>(() => Object.entries(selected.value?.inputs ?? {}))

watch(() => props.modelValue, (value) => {
  enabled.value = Boolean(value) || enabled.value
  hydrateFromPipeline(value)
}, { immediate: true })

watch(selected, (template) => {
  if (hydratingPipeline) return
  resetVars(template)
}, { flush: 'sync' })

function resetVars(template?: PipelineTemplateSummary, values: Record<string, string> = {}) {
  for (const key of Object.keys(vars)) delete vars[key]
  for (const [name, input] of Object.entries(template?.inputs ?? {})) {
    vars[name] = values[name] ?? input.default ?? ''
  }
}

function templateKey(template: PipelineTemplateSummary) {
  return `${template.source}://${template.id}@${template.version}`
}

function hydrateFromPipeline(pipeline?: Pipeline) {
  const include = pipeline?.build?.find(s => s.type === 'include')
    ?? pipeline?.deploy?.find(s => s.type === 'include')
    ?? pipeline?.finally?.find(s => s.type === 'include')
  if (!include?.with) return

  const templateURI = typeof include.with.template === 'string' ? include.with.template : ''
  const version = typeof include.with.version === 'string' ? include.with.version : ''
  const rawVars = include.with.vars && typeof include.with.vars === 'object'
    ? include.with.vars as Record<string, unknown>
    : {}
  const restoredVars = Object.fromEntries(Object.entries(rawVars).map(([key, value]) => [key, String(value)]))
  const key = version ? `${templateURI}@${version}` : ''
  const template = props.templates.find(t => templateKey(t) === key)

  hydratingPipeline = true
  selectedKey.value = key
  resetVars(template, restoredVars)
  hydratingPipeline = false
}

function enable() {
  enabled.value = true
  if (!selectedKey.value && props.templates[0]) {
    selectedKey.value = templateKey(props.templates[0])
  }
}

function disable() {
  enabled.value = false
  selectedKey.value = ''
  emit('update:modelValue', undefined)
}

function saveTemplate() {
  if (!selected.value) return
  const template = selected.value
  emit('update:modelValue', {
    deploy: [{
      name: template.name,
      type: 'include',
      with: {
        template: `${template.source}://${template.id}`,
        version: template.version,
        digest: template.digest,
        vars: { ...vars },
      },
    }],
  })
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
        <label class="field-label">选择模板</label>
        <select v-model="selectedKey" class="field-input" data-test="template-select">
          <option value="" disabled>请选择模板</option>
          <option v-for="template in templates" :key="templateKey(template)" :value="templateKey(template)">
            {{ template.name }} · {{ template.source }} · {{ template.version }}
          </option>
        </select>

        <div v-if="selected?.description" class="template-description">{{ selected.description }}</div>

        <div v-for="[name, input] in inputEntries" :key="name" class="template-input-row">
          <label class="field-label" :for="`template-input-${name}`">
            {{ input.label || name }}<span v-if="input.required" class="required">*</span>
          </label>
          <select
            v-if="input.type === 'select'"
            :id="`template-input-${name}`"
            v-model="vars[name]"
            class="field-input"
            :data-test="`template-input-${name}`"
          >
            <option v-for="option in input.options ?? []" :key="option" :value="option">{{ option }}</option>
          </select>
          <input
            v-else
            :id="`template-input-${name}`"
            v-model="vars[name]"
            class="field-input"
            :type="input.type === 'number' ? 'number' : 'text'"
            :data-test="`template-input-${name}`"
          />
          <div v-if="input.description" class="field-help">{{ input.description }}</div>
        </div>

        <button type="button" class="pipeline-save" data-test="pipeline-save-template" :disabled="!selected" @click="saveTemplate">
          使用模板
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
.pipeline-save:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.wizard-head {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 6px;
}
.pipeline-disable {
  background: transparent;
  border: none;
  color: var(--status-failed);
  cursor: pointer;
  font-size: 11px;
}
.pipeline-empty,
.template-description,
.field-help {
  font-size: 11px;
  color: var(--text-tertiary);
}
.preview-error {
  margin-top: 8px;
  font-size: 11px;
  color: var(--status-failed);
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
</style>
