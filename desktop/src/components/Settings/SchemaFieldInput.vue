<!--
SchemaFieldInput：按后端 RuntimeSchema 渲染单个配置字段。

职责：
  - 根据 schema field 类型渲染字符串、布尔、数字和字符串数组输入
  - 使用 schema 下发的本地化文案展示字段名和说明
  - 将用户输入按字段类型转换后向父组件 emit

边界：
  - 不拉取 schema、不保存 deployment
  - 不执行服务运行时校验，只展示父组件传入的 diagnostics
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { LocalizedText, RuntimeDiagnostic, RuntimeSchemaField } from '@/api/agent'
import { currentLocale } from '@/i18n'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  field: RuntimeSchemaField
  value?: unknown
  diagnostics?: RuntimeDiagnostic[]
}>()
const emit = defineEmits<{ 'update:value': [unknown] }>()
const { t } = useAppI18n()

const label = computed(() => localizedText(props.field.name))
const description = computed(() => localizedText(props.field.desc))
const fieldDiagnostics = computed(() => (props.diagnostics ?? []).filter(d => d.field === props.field.key))
const arrayValue = computed(() => Array.isArray(props.value) ? props.value.map(v => String(v)) : [])

function localizedText(text: LocalizedText) {
  const locale = currentLocale()
  return text.values?.[locale] ?? text.default
}

function updateNumber(raw: string) {
  emit('update:value', raw === '' ? undefined : Number(raw))
}

function updateArray(index: number, value: string) {
  const next = [...arrayValue.value]
  next[index] = value
  emit('update:value', next)
}

function addArrayValue() {
  emit('update:value', [...arrayValue.value, ''])
}

function removeArrayValue(index: number) {
  const next = [...arrayValue.value]
  next.splice(index, 1)
  emit('update:value', next)
}
</script>

<template>
  <div class="settings-field schema-field">
    <label class="settings-field-label dep-label" :for="`schema-field-${field.key}`">
      {{ label }}
      <span v-if="field.required" class="schema-required">*</span>
    </label>
    <div v-if="description" class="dep-help schema-desc">{{ description }}</div>

    <input
      v-if="field.type === 'string'"
      :id="`schema-field-${field.key}`"
      class="settings-input dep-input"
      :data-test="`schema-field-${field.key}`"
      :value="typeof value === 'string' ? value : ''"
      @input="emit('update:value', ($event.target as HTMLInputElement).value)"
    />

    <label v-else-if="field.type === 'boolean'" class="dep-choice schema-checkbox">
      <input
        :id="`schema-field-${field.key}`"
        type="checkbox"
        :data-test="`schema-field-${field.key}`"
        :checked="Boolean(value)"
        @change="emit('update:value', ($event.target as HTMLInputElement).checked)"
      />
    </label>

    <input
      v-else-if="field.type === 'number'"
      :id="`schema-field-${field.key}`"
      class="settings-input dep-input"
      type="number"
      :data-test="`schema-field-${field.key}`"
      :value="typeof value === 'number' ? value : ''"
      @input="updateNumber(($event.target as HTMLInputElement).value)"
    />

    <div v-else class="schema-array">
      <div v-for="(item, index) in arrayValue" :key="index" class="schema-array-row">
        <input
          class="settings-input dep-input"
          :data-test="`schema-field-${field.key}-${index}`"
          :value="item"
          @input="updateArray(index, ($event.target as HTMLInputElement).value)"
        />
        <button
          type="button"
          class="settings-btn settings-btn-danger schema-array-remove"
          :data-test="`schema-field-${field.key}-remove-${index}`"
          @click="removeArrayValue(index)"
        >
          {{ t('common.remove') }}
        </button>
      </div>
      <button
        type="button"
        class="settings-btn settings-btn-secondary schema-array-add"
        :data-test="`schema-field-${field.key}-add`"
        @click="addArrayValue"
      >
        {{ t('common.add') }}
      </button>
    </div>

    <div v-for="diagnostic in fieldDiagnostics" :key="diagnostic.code" class="schema-diagnostic" :class="`schema-diagnostic-${diagnostic.severity}`">
      {{ diagnostic.message }}
    </div>
  </div>
</template>

<style scoped>
.schema-field {
  margin-top: 8px;
}
.schema-required {
  color: var(--status-failed);
  margin-left: 2px;
}
.schema-desc {
  margin-bottom: 4px;
}
.schema-checkbox {
  margin-top: 4px;
}
.schema-array-row {
  display: flex;
  gap: 6px;
  align-items: center;
  margin-bottom: 4px;
}
.schema-array-remove {
  min-width: 56px;
}
.schema-array-add {
  margin-top: 4px;
}
.schema-diagnostic {
  margin-top: 4px;
  font-size: 11px;
  line-height: 1.4;
}
.schema-diagnostic-error {
  color: var(--status-failed);
}
.schema-diagnostic-warning {
  color: var(--warning);
}
.schema-diagnostic-info {
  color: var(--text-tertiary);
}
</style>
