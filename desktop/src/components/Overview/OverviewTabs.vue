<!--
OverviewTabs：项目概览页的顶层视图切换。

职责：
  - 在运行状态和流水线之间切换
  - 暴露稳定 data-test 供组件测试定位

边界：
  - 不拉取数据
  - 不负责路由跳转
-->
<script setup lang="ts">
import { useAppI18n } from '@/i18n/useAppI18n'

defineProps<{ modelValue: 'runtime' | 'pipelines' }>()
const emit = defineEmits<{ 'update:modelValue': ['runtime' | 'pipelines'] }>()
const { t } = useAppI18n()
</script>

<template>
  <div class="overview-tabs">
    <button
      type="button"
      data-test="overview-tab-runtime"
      :class="{ active: modelValue === 'runtime' }"
      @click="emit('update:modelValue', 'runtime')"
    >
      {{ t('overview.runtime') }}
    </button>
    <button
      type="button"
      data-test="overview-tab-pipelines"
      :class="{ active: modelValue === 'pipelines' }"
      @click="emit('update:modelValue', 'pipelines')"
    >
      {{ t('overview.pipelines') }}
    </button>
  </div>
</template>

<style scoped>
.overview-tabs {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
}
.overview-tabs button {
  min-width: 86px;
  height: 30px;
  padding: 0 12px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}
.overview-tabs button.active {
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.overview-tabs button:hover {
  color: var(--text-primary);
}
</style>
