<!--
OverviewTabs：项目概览页的顶层视图切换。

职责：
  - 在运行状态、流水线和入口配置之间切换
  - 暴露稳定 data-test 供组件测试定位

边界：
  - 不拉取数据
  - 不负责路由跳转
-->
<script setup lang="ts">
import { useAppI18n } from '@/i18n/useAppI18n'

type OverviewTab = 'runtime' | 'pipelines' | 'ingress'

defineProps<{ modelValue: OverviewTab }>()
const emit = defineEmits<{ 'update:modelValue': [OverviewTab] }>()
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
    <button
      type="button"
      data-test="overview-tab-ingress"
      :class="{ active: modelValue === 'ingress' }"
      @click="emit('update:modelValue', 'ingress')"
    >
      {{ t('overview.ingress.tab') }}
    </button>
  </div>
</template>

<style scoped>
.overview-tabs {
  display: inline-flex;
  gap: 2px;
  padding: 4px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: rgba(16, 22, 31, 0.72);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
}
.overview-tabs button {
  min-width: 122px;
  height: 44px;
  padding: 0 18px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 14px;
  font-weight: 800;
}
.overview-tabs button.active {
  background: rgba(38, 46, 58, 0.9);
  color: var(--text-primary);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.04);
}
.overview-tabs button:hover {
  color: var(--text-primary);
}
</style>
