<!--
PivotToolbar：运行状态页的维度分组切换栏。

职责：
  - 展示一级/二级维度(服务/环境/节点)两组 segmented 按钮
  - 当前 primary 在二级中置灰, 避免选到同一维度
  - 任一改动 emit change(primary, secondary)交由父组件写偏好

边界：
  - 不直接读写 settingsStore(由父组件接线)
  - 不渲染分组结果
-->
<script setup lang="ts">
import type { Dimension } from '@/lib/runtimePivot'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ primary: Dimension; secondary: Dimension }>()
const emit = defineEmits<{ change: [primary: Dimension, secondary: Dimension] }>()
const { t } = useAppI18n()

const dims: Dimension[] = ['service', 'env', 'node']

function label(dim: Dimension) {
  return t(`overview.runtimeStatus.dimension.${dim}`)
}

function pickPrimary(dim: Dimension) {
  emit('change', dim, props.secondary)
}

function pickSecondary(dim: Dimension) {
  if (dim === props.primary) return
  emit('change', props.primary, dim)
}
</script>

<template>
  <div class="pivot-toolbar">
    <span class="pivot-label">{{ t('overview.runtimeStatus.groupBy') }}</span>
    <div class="seg">
      <button
        v-for="dim in dims"
        :key="`p-${dim}`"
        type="button"
        :data-test="`primary-${dim}`"
        :class="{ on: dim === props.primary }"
        @click="pickPrimary(dim)"
      >
        {{ label(dim) }}
      </button>
    </div>
    <span class="pivot-arrow">▸</span>
    <div class="seg">
      <button
        v-for="dim in dims"
        :key="`s-${dim}`"
        type="button"
        :data-test="`secondary-${dim}`"
        :disabled="dim === props.primary"
        :class="{ on: dim === props.secondary }"
        @click="pickSecondary(dim)"
      >
        {{ label(dim) }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.pivot-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 14px;
  padding: 8px 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
}

.pivot-label {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
}

.pivot-arrow {
  color: var(--text-tertiary);
}

.seg {
  display: inline-flex;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
}

.seg button {
  padding: 4px 10px;
  border: none;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-primary);
  color: var(--text-tertiary);
  font-size: 12px;
  cursor: pointer;
}

.seg button:last-child {
  border-right: none;
}

.seg button.on {
  background: var(--accent, #1f6feb);
  color: #fff;
  font-weight: 600;
}

.seg button:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
</style>
