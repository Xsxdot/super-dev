<!--
DebugCredentialEditor：AI 调试凭据行编辑器。

职责：
  - 编辑 debug_credentials 的 name/value/desc 行
  - 新增、删除凭据，并在每次变更时向父层 emit 完整数组
  - 对 value 默认使用密码输入，允许本地临时显示
边界：
  - 不调用 API，不做持久化
  - 不在 overview/list 等普通运行视图展示凭据明文
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { DebugCredential } from '@/api/agent'

const props = defineProps<{
  modelValue: DebugCredential[]
  title: string
  hint?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [DebugCredential[]] }>()
const { t } = useAppI18n()

const rows = ref<DebugCredential[]>([])
const visibleRows = ref<Record<number, boolean>>({})

watch(
  () => props.modelValue,
  val => {
    rows.value = (val ?? []).map(c => ({ ...c }))
    visibleRows.value = {}
  },
  { immediate: true },
)

function emitRows() {
  emit('update:modelValue', rows.value.map(c => ({ ...c })))
}

function addRow() {
  rows.value.push({ name: '', value: '', desc: '' })
  emitRows()
}

function deleteRow(index: number) {
  rows.value.splice(index, 1)
  delete visibleRows.value[index]
  emitRows()
}

function toggleVisible(index: number) {
  visibleRows.value[index] = !visibleRows.value[index]
}
</script>

<template>
  <section class="debug-credential-editor">
    <div class="debug-credential-head">
      <div>
        <h3 class="debug-credential-title">{{ title }}</h3>
        <p v-if="hint" class="debug-credential-hint">{{ hint }}</p>
      </div>
      <button type="button" class="settings-btn settings-btn-secondary" data-test="debug-credential-add" @click="addRow">
        {{ t('settings.debugCredentials.add') }}
      </button>
    </div>

    <div v-if="rows.length === 0" class="debug-credential-empty">
      {{ t('settings.debugCredentials.empty') }}
    </div>

    <div v-for="(row, i) in rows" :key="i" class="debug-credential-row" data-test="debug-credential-row">
      <input
        v-model="row.name"
        class="settings-input debug-credential-name"
        data-test="debug-credential-name"
        :placeholder="t('settings.debugCredentials.namePlaceholder')"
        @input="emitRows"
      />
      <div class="debug-credential-value-wrap">
        <input
          v-model="row.value"
          class="settings-input debug-credential-value"
          data-test="debug-credential-value"
          :type="visibleRows[i] ? 'text' : 'password'"
          :placeholder="t('settings.debugCredentials.valuePlaceholder')"
          @input="emitRows"
        />
        <button
          type="button"
          class="settings-btn debug-credential-toggle"
          data-test="debug-credential-toggle"
          @click="toggleVisible(i)"
        >
          {{ visibleRows[i] ? t('settings.debugCredentials.hide') : t('settings.debugCredentials.show') }}
        </button>
      </div>
      <input
        v-model="row.desc"
        class="settings-input debug-credential-desc"
        data-test="debug-credential-desc"
        :placeholder="t('settings.debugCredentials.descPlaceholder')"
        @input="emitRows"
      />
      <button
        type="button"
        class="settings-btn settings-btn-danger debug-credential-delete"
        data-test="debug-credential-delete"
        @click="deleteRow(i)"
      >
        {{ t('common.delete') }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.debug-credential-editor {
  display: grid;
  gap: 8px;
  border-top: 1px solid var(--border-secondary);
  padding: 10px 0 0;
  margin-top: 10px;
}

.debug-credential-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.debug-credential-title {
  margin: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 600;
}

.debug-credential-hint,
.debug-credential-empty {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
}

.debug-credential-row {
  display: grid;
  grid-template-columns: minmax(110px, 0.9fr) minmax(160px, 1.1fr) minmax(160px, 1.2fr) auto;
  gap: 6px;
  align-items: center;
}

.debug-credential-value-wrap {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 6px;
  min-width: 0;
}

.debug-credential-name,
.debug-credential-value,
.debug-credential-desc {
  min-width: 0;
}

.debug-credential-toggle,
.debug-credential-delete {
  white-space: nowrap;
}

@media (max-width: 760px) {
  .debug-credential-head,
  .debug-credential-row {
    display: grid;
    grid-template-columns: 1fr;
  }
}
</style>
