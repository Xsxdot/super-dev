<script setup lang="ts">
// LogPinBadge 渲染日志行左侧的证据钉子标记。
//
// 职责：
//   - 展示 pin 序号、颜色和备注状态
//   - 将编辑意图通过事件交给父组件
//
// 边界：
//   - 不修改 pin 数据
//   - 不打开导出抽屉
import type { EvidencePin } from '@/stores/logEvidence'

const props = defineProps<{
  pin: EvidencePin
}>()

const emit = defineEmits<{
  edit: [pin: EvidencePin, event: MouseEvent]
}>()
</script>

<template>
  <button
    type="button"
    class="log-pin-badge"
    data-test="log-pin-badge"
    :style="{ '--pin-color': props.pin.color }"
    :title="props.pin.note || props.pin.label"
    @click.stop="emit('edit', props.pin, $event)"
  >
    <span class="pin-label">{{ props.pin.label }}</span>
    <span
      v-if="props.pin.note.trim()"
      class="pin-note-dot"
      data-test="log-pin-note-indicator"
      aria-hidden="true"
    />
  </button>
</template>

<style scoped>
.log-pin-badge {
  width: 26px;
  min-width: 26px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 0;
  border: 1px solid color-mix(in srgb, var(--pin-color) 70%, transparent);
  border-radius: 4px;
  background: color-mix(in srgb, var(--pin-color) 18%, transparent);
  color: var(--pin-color);
  cursor: pointer;
  font-family: inherit;
  font-size: 10px;
  font-weight: 750;
  line-height: 1;
}

.log-pin-badge:hover {
  background: color-mix(in srgb, var(--pin-color) 28%, transparent);
}

.pin-label {
  line-height: 1;
}

.pin-note-dot {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: currentColor;
}
</style>
