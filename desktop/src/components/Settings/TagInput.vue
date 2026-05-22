<!--
TagInput：标签式多值输入组件。

职责：
  - 已添加的 tag 以彩色 chip 形式展示，点击 × 可删除
  - 输入框内按 Enter 或逗号触发添加，自动 trim 并去重
  - 通过 v-model 双向绑定 tags 数组

边界：
  - 不负责持久化，只管理当前表单状态
  - 颜色由 tagColor 工具函数决定，与 HostManagerTab 保持一致
-->
<script setup lang="ts">
import { ref } from 'vue'
import { tagColor } from '@/lib/tagColor'

const props = defineProps<{ modelValue: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()

const input = ref('')

function addTag(raw: string) {
  const trimmed = raw.trim()
  if (!trimmed || props.modelValue.includes(trimmed)) return
  emit('update:modelValue', [...props.modelValue, trimmed])
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    addTag(input.value)
    input.value = ''
  }
}

function onBlur() {
  if (input.value.trim()) {
    addTag(input.value)
    input.value = ''
  }
}

function removeTag(tag: string) {
  emit('update:modelValue', props.modelValue.filter(t => t !== tag))
}
</script>

<template>
  <div class="tag-input">
    <span
      v-for="tag in modelValue"
      :key="tag"
      class="chip"
      :style="{ background: tagColor(tag) }"
    >
      {{ tag }}
      <button class="remove" type="button" @click="removeTag(tag)">×</button>
    </span>
    <input
      v-model="input"
      class="tag-text"
      placeholder="输入后按 Enter 添加"
      @keydown="onKeydown"
      @blur="onBlur"
    />
  </div>
</template>

<style scoped>
.tag-input {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  align-items: center;
  min-height: 32px;
  padding: 4px 6px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 6px;
  color: #fff;
  border-radius: 2px;
  font-size: 10px;
}
.remove {
  padding: 0;
  color: inherit;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  opacity: 0.7;
}
.remove:hover {
  opacity: 1;
}
.tag-text {
  flex: 1;
  min-width: 80px;
  padding: 0 2px;
  color: var(--text-primary);
  background: transparent;
  border: none;
  font-size: 12px;
  outline: none;
}
</style>
