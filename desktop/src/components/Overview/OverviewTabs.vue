<!--
OverviewTabs：项目概览页的顶层视图切换。

职责：
  - 在运行状态、流水线、入口配置和项目配置之间切换
  - 暴露稳定 data-test 供组件测试定位

边界：
  - 不拉取数据
  - 不负责路由跳转
-->
<script setup lang="ts">
import { useAppI18n } from '@/i18n/useAppI18n'

type OverviewTab = 'runtime' | 'pipelines' | 'ingress' | 'config'

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
    <button
      type="button"
      data-test="overview-tab-config"
      :class="{ active: modelValue === 'config' }"
      @click="emit('update:modelValue', 'config')"
    >
      {{ t('overview.config') }}
    </button>
  </div>
</template>

<style scoped>
.overview-tabs {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  width: min(430px, 100%);
  height: 44px;
  gap: 0;
  padding: 3px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: #0e141d;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
}
.overview-tabs button {
  min-width: 0;
  height: 36px;
  padding: 0 12px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 13px;
  font-weight: 650;
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
