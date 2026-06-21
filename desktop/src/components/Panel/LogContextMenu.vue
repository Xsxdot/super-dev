<script setup lang="ts">
// LogContextMenu 承载日志行右键菜单命令。
//
// 职责：
//   - 展示复制、带 cursor 复制、打钉/取消、时间对齐入口
//   - 将菜单命令通过事件交给 LogPanel
//
// 边界：
//   - 不访问剪贴板
//   - 不修改 pin store
import { onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  x: number
  y: number
  hasPin: boolean
  canAlign: boolean
}>()

const emit = defineEmits<{
  'copy-log': []
  'copy-log-with-cursor': []
  'add-pin': []
  'remove-pin': []
  'align-time': []
  close: []
}>()

const { t } = useI18n()

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close')
}

function onPointerDown(event: PointerEvent) {
  const target = event.target as HTMLElement | null
  if (target?.closest('[data-test="log-context-menu"]')) return
  emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('pointerdown', onPointerDown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('pointerdown', onPointerDown)
})
</script>

<template>
  <div
    class="log-context-menu"
    data-test="log-context-menu"
    :style="{ left: `${props.x}px`, top: `${props.y}px` }"
    @contextmenu.prevent
  >
    <button type="button" class="menu-item" data-test="copy-log" @click="emit('copy-log')">{{ t('panel.evidence.copyLog') }}</button>
    <button type="button" class="menu-item" data-test="copy-log-with-cursor" @click="emit('copy-log-with-cursor')">{{ t('panel.evidence.copyWithCursor') }}</button>
    <button v-if="!props.hasPin" type="button" class="menu-item" data-test="add-pin" @click="emit('add-pin')">{{ t('panel.evidence.pin') }}</button>
    <button v-else type="button" class="menu-item" data-test="remove-pin" @click="emit('remove-pin')">{{ t('panel.evidence.unpin') }}</button>
    <button
      v-if="props.canAlign"
      type="button"
      class="menu-item"
      data-test="align-time"
      @click="emit('align-time')"
    >
      {{ t('panel.evidence.alignOtherPanes') }}
    </button>
  </div>
</template>

<style scoped>
.log-context-menu {
  position: fixed;
  z-index: 40;
  min-width: 168px;
  padding: 5px;
  border: 1px solid rgba(88, 166, 255, 0.34);
  border-radius: 6px;
  background: rgba(13, 24, 34, 0.98);
  box-shadow: 0 14px 30px rgba(0, 0, 0, 0.36);
}

.menu-item {
  width: 100%;
  height: 28px;
  display: flex;
  align-items: center;
  padding: 0 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  text-align: left;
}

.menu-item:hover {
  background: rgba(88, 166, 255, 0.12);
  color: var(--text-primary);
}
</style>
