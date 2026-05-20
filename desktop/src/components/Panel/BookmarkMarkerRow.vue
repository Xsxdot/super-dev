<script setup lang="ts">
defineProps<{
  isStart: boolean
  date: Date
}>()

function formatTime(d: Date) {
  return d.toLocaleTimeString('en-US', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}
</script>

<template>
  <div class="bookmark-marker">
    <span class="label">{{ isStart ? '▶ 开始标记' : '■ 结束标记' }}</span>
    <span class="time">{{ formatTime(date) }}</span>
  </div>
</template>

<style scoped>
.bookmark-marker {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 10px;
  margin: 2px 6px;
  border-radius: 3px;
  background: rgba(120, 56, 10, 0.35);
  border: 1px solid rgba(210, 120, 40, 0.4);
  font-size: 11px;
  font-family: 'SF Mono', 'Cascadia Code', monospace;
}
.label {
  color: #ffa657;
  font-weight: 600;
}
.time {
  color: var(--text-tertiary);
  font-size: 10px;
}
</style>
