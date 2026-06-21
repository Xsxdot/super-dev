<script setup lang="ts">
// PinNotePopover 提供证据钉子的轻量备注编辑。
//
// 职责：
//   - 管理当前备注草稿
//   - 在用户确认时提交备注文本
//
// 边界：
//   - 不决定 pin 的增删
//   - 不处理导出格式
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { EvidencePin } from '@/stores/logEvidence'

const props = defineProps<{
  pin: EvidencePin
}>()

const emit = defineEmits<{
  save: [note: string]
  close: []
}>()

const { t } = useI18n()
const draft = ref(props.pin.note)

watch(
  () => props.pin.id,
  () => {
    draft.value = props.pin.note
  },
)

function save() {
  emit('save', draft.value)
}
</script>

<template>
  <div class="pin-note-popover" data-test="pin-note-popover">
    <div class="pin-note-head">
      <span class="pin-sequence" :style="{ color: props.pin.color }">{{ props.pin.label }}</span>
      <span class="pin-cursor">{{ props.pin.log.id }}</span>
    </div>
    <textarea
      v-model="draft"
      class="pin-note-input"
      data-test="pin-note-input"
      :placeholder="t('panel.evidence.notePlaceholder')"
      rows="4"
      @keydown.meta.enter.prevent="save"
      @keydown.ctrl.enter.prevent="save"
    />
    <div class="pin-note-actions">
      <button type="button" class="pin-note-btn" data-test="pin-note-cancel" @click="emit('close')">{{ t('panel.evidence.cancel') }}</button>
      <button type="button" class="pin-note-btn primary" data-test="pin-note-save" @click="save">{{ t('panel.evidence.save') }}</button>
    </div>
  </div>
</template>

<style scoped>
.pin-note-popover {
  position: fixed;
  z-index: 950;
  width: 260px;
  padding: 8px;
  border: 1px solid rgba(88, 166, 255, 0.34);
  border-radius: 6px;
  background: rgba(13, 24, 34, 0.98);
  box-shadow: 0 12px 26px rgba(0, 0, 0, 0.36);
}

.pin-note-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
  font-size: 11px;
}

.pin-sequence {
  font-weight: 750;
}

.pin-cursor {
  color: var(--text-tertiary);
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
}

.pin-note-input {
  width: 100%;
  resize: vertical;
  min-height: 72px;
  padding: 6px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 12px;
  outline: none;
}

.pin-note-actions {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
  margin-top: 7px;
}

.pin-note-btn {
  height: 24px;
  padding: 0 9px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
}

.pin-note-btn.primary {
  border-color: rgba(88, 166, 255, 0.38);
  color: #58a6ff;
}
</style>
