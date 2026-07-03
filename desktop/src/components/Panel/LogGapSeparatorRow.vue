<!--
断流缺口分隔行

职责：
  - 在断线补拉封顶或失败时，明示此处可能缺失日志

边界：
  - 不代表真实 LogEntry
  - 只展示不交互（期 2 协议版将支持点击补拉重试）
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ time: string }>()
const { t } = useAppI18n()

const displayTime = computed(() => new Date(props.time).toLocaleTimeString())
</script>

<template>
  <div class="gap-separator-row">
    <span class="line" />
    <span class="label">! {{ t('panel.log.gapSeparator', { time: displayTime }) }}</span>
    <span class="line" />
  </div>
</template>

<style scoped>
.gap-separator-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  color: #d29922;
  font-size: 10px;
}
.line {
  height: 1px;
  flex: 1;
  border-top: 1px dashed rgba(210, 153, 34, 0.45);
}
.label {
  white-space: nowrap;
}
</style>
