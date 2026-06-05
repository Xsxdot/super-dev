<!--
WorkDirInput：工作目录输入框（含文件夹选择按钮）。

职责：
  - 渲染可编辑的路径输入框
  - 右侧按钮调用 Tauri dialog 选择目录，选中后更新值
  - 支持外部传入默认路径提示

边界：
  - 不做路径合法性校验（由上层 validateDraft 处理）
  - 不依赖具体业务，通用于任何路径选择场景
-->
<script setup lang="ts">
import { open } from '@tauri-apps/plugin-dialog'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  modelValue?: string
  placeholder?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [string] }>()
const { t } = useAppI18n()

async function pickDir() {
  const selected = await open({
    directory: true,
    multiple: false,
    title: t('settings.workDir.selectTitle'),
    defaultPath: props.modelValue || undefined,
  })
  if (selected && typeof selected === 'string') {
    emit('update:modelValue', selected)
  }
}
</script>

<template>
  <div class="wdi-wrap">
    <input
      class="settings-input wdi-input"
      :placeholder="placeholder ?? t('settings.workDir.placeholder')"
      :value="modelValue"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <button type="button" class="settings-btn settings-btn-secondary settings-btn-icon wdi-pick" :title="t('settings.workDir.pickFolder')" @click="pickDir">
      <svg width="13" height="13" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
        <path d="M1 3.5A1.5 1.5 0 0 1 2.5 2h3.086a1.5 1.5 0 0 1 1.06.44l.915.914A1.5 1.5 0 0 0 8.62 3.8H13.5A1.5 1.5 0 0 1 15 5.3v7.2A1.5 1.5 0 0 1 13.5 14h-11A1.5 1.5 0 0 1 1 12.5v-9Z" stroke="currentColor" stroke-width="1.2" fill="none"/>
      </svg>
    </button>
  </div>
</template>

<style scoped>
.wdi-wrap {
  display: flex;
  align-items: stretch;
  width: 100%;
}
.wdi-input {
  flex: 1;
  min-width: 0;
  border-right: none;
  border-radius: 6px 0 0 6px;
}
.wdi-pick {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0 6px 6px 0;
}
</style>
