<script setup lang="ts">
import { computed } from 'vue'
import type { LogEntry } from '@/api/agent'

const props = defineProps<{
  log: LogEntry
  highlighted: boolean  // 书签录制区间内高亮
}>()

const SERVICE_COLORS = ['#58a6ff','#bc8cff','#f78166','#ffa657','#7ce38b','#39d353','#a5d6ff','#ff7b72']
function serviceColor(serviceId: string) {
  let hash = 0
  for (const c of serviceId) hash = (hash * 31 + c.charCodeAt(0)) & 0xffffffff
  return SERVICE_COLORS[Math.abs(hash) % SERVICE_COLORS.length]
}

const levelColor = computed(() => {
  if (props.log.level === 'ERROR') return '#f85149'
  if (props.log.level === 'WARN') return '#d29922'
  if (props.log.level === 'DEBUG') return '#6e7681'
  return '#3fb950'
})

const rowBg = computed(() => {
  if (props.highlighted) {
    if (props.log.level === 'ERROR') return 'rgba(248,81,73,0.18)'
    if (props.log.level === 'WARN') return 'rgba(210,153,34,0.12)'
    return 'rgba(30,25,10,0.5)'
  }
  if (props.log.level === 'ERROR') return 'rgba(248,81,73,0.10)'
  if (props.log.level === 'WARN') return 'rgba(210,153,34,0.07)'
  return 'transparent'
})

const time = computed(() => {
  const d = new Date(props.log.timestamp)
  return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
})
</script>

<template>
  <div class="log-row" :style="{ background: rowBg }">
    <span class="ts">{{ time }}</span>
    <span class="svc" :style="{ color: serviceColor(log.service_id) }">[{{ log.service_id.slice(0, 12) }}]</span>
    <span class="level" :style="{ color: levelColor }">{{ log.level.padEnd(5) }}</span>
    <span class="msg">{{ log.message }}</span>
  </div>
</template>

<style scoped>
.log-row {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 1px 8px;
  border-radius: 2px;
  font-size: 11px;
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.ts { color: var(--text-tertiary); flex-shrink: 0; }
.svc { flex-shrink: 0; }
.level { flex-shrink: 0; width: 48px; }
.msg { flex: 1; color: var(--text-primary); }
</style>
