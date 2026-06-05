<!--
EnvKeyValueEditor：环境变量 key-value 行编辑器。

职责：
  - 把 Record<string,string> 展示为可增删的 key/value 行
  - 每次变更 emit 重建后的对象（空 key 行在父层拍平时忽略）
边界：
  - 不做校验，不发请求
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ modelValue: Record<string, string> }>()
const emit = defineEmits<{ 'update:modelValue': [Record<string, string>] }>()
const { t } = useAppI18n()

interface Row { key: string; value: string }
const rows = ref<Row[]>([])

// 外部值变化时同步行（仅在引用变化时重建，避免编辑时被打断）
watch(
  () => props.modelValue,
  val => {
    rows.value = Object.entries(val ?? {}).map(([key, value]) => ({ key, value }))
  },
  { immediate: true },
)

function emitRows() {
  const out: Record<string, string> = {}
  for (const r of rows.value) {
    out[r.key] = r.value
  }
  emit('update:modelValue', out)
}

function addRow() {
  rows.value.push({ key: '', value: '' })
}

function delRow(i: number) {
  rows.value.splice(i, 1)
  emitRows()
}
</script>

<template>
  <div class="env-editor">
    <div v-for="(row, i) in rows" :key="i" class="env-row" data-test="env-row">
      <input v-model="row.key" class="settings-input env-input" placeholder="KEY" @input="emitRows" />
      <span class="env-eq">=</span>
      <input v-model="row.value" class="settings-input env-input" placeholder="VALUE" @input="emitRows" />
      <button type="button" class="settings-btn settings-btn-danger env-del" data-test="env-del" @click="delRow(i)">✕</button>
    </div>
    <button type="button" class="settings-btn settings-btn-secondary env-add" data-test="env-add" @click="addRow">
      {{ t('settings.deployment.addEnvVar') }}
    </button>
  </div>
</template>

<style scoped>
.env-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
}
.env-input {
  flex: 1;
}
.env-eq {
  color: var(--text-tertiary);
}
.env-del {
  min-width: 28px;
}
.env-add {
  margin-top: 4px;
}
</style>
