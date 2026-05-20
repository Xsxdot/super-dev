<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const props = defineProps<{
  text: string
}>()

const emit = defineEmits<{
  'selection-change': [text: string | null, rect: DOMRect | null]
}>()

const elRef = ref<HTMLElement | null>(null)

function reportSelection() {
  const sel = window.getSelection()
  if (!sel || sel.isCollapsed || !elRef.value) {
    emit('selection-change', null, null)
    return
  }
  const range = sel.rangeCount > 0 ? sel.getRangeAt(0) : null
  if (!range || !elRef.value.contains(range.commonAncestorContainer)) {
    emit('selection-change', null, null)
    return
  }
  const text = sel.toString().trim()
  if (!text) {
    emit('selection-change', null, null)
    return
  }
  const rect = range.getBoundingClientRect()
  emit('selection-change', text, rect)
}

function onMouseUp() {
  requestAnimationFrame(reportSelection)
}

function onSelectionChange() {
  reportSelection()
}

onMounted(() => {
  document.addEventListener('selectionchange', onSelectionChange)
})

onUnmounted(() => {
  document.removeEventListener('selectionchange', onSelectionChange)
})
</script>

<template>
  <span ref="elRef" class="selectable-msg" @mouseup="onMouseUp">{{ props.text }}</span>
</template>

<style scoped>
.selectable-msg {
  flex: 1;
  color: var(--text-primary);
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
</style>
