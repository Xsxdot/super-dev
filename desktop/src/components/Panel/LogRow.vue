<script setup lang="ts">
import { computed } from 'vue'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { EvidencePin } from '@/stores/logEvidence'
import LogPinBadge from './LogPinBadge.vue'
import SelectableLogText from './SelectableLogText.vue'

const props = defineProps<{
  log: DisplayLogEntry
  serviceName: string
  highlighted: boolean
  evidencePin?: EvidencePin | null
  evidenceFlash?: boolean
  timeAnchor?: boolean
}>()

const emit = defineEmits<{
  'selection-change': [text: string | null, rect: DOMRect | null]
  'toggle-pin': [log: DisplayLogEntry]
  'edit-pin': [pin: EvidencePin, event: MouseEvent]
  'row-context-menu': [event: MouseEvent, log: DisplayLogEntry]
}>()

const SERVICE_COLORS = ['#58a6ff', '#bc8cff', '#f78166', '#ffa657', '#7ce38b', '#39d353', '#a5d6ff', '#ff7b72']

function serviceColor(name: string) {
  let hash = 0
  for (const c of name) hash = (hash * 31 + c.charCodeAt(0)) & 0xffffffff
  return SERVICE_COLORS[Math.abs(hash) % SERVICE_COLORS.length]
}

const levelColor = computed(() => {
  if (props.log.level === 'ERROR') return '#f85149'
  if (props.log.level === 'WARN') return '#d29922'
  if (props.log.level === 'DEBUG') return '#6e7681'
  return '#3fb950'
})

const rowBg = computed(() => {
  if (props.evidenceFlash) return 'rgba(88,166,255,0.18)'
  if (props.timeAnchor) return 'rgba(54,207,201,0.12)'
  if (props.highlighted) {
    if (props.log.level === 'ERROR') return 'rgba(248,81,73,0.15)'
    if (props.log.level === 'WARN') return 'rgba(210,153,34,0.10)'
    return 'rgba(88,166,255,0.08)'
  }
  if (props.log.level === 'ERROR') return 'rgba(248,81,73,0.085)'
  if (props.log.level === 'WARN') return 'rgba(210,153,34,0.055)'
  return 'transparent'
})

const time = computed(() => {
  const d = new Date(props.log.timestamp)
  return d.toLocaleTimeString('en-US', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
})

const repeatCount = computed(() => props.log.repeat_count ?? 1)
</script>

<template>
  <div
    class="log-row"
    :class="{ 'has-pin': !!evidencePin, 'is-time-anchor': !!timeAnchor }"
    :style="{ background: rowBg }"
    :data-log-id="log.id"
    @dblclick.stop="emit('toggle-pin', log)"
    @contextmenu.prevent="emit('row-context-menu', $event, log)"
  >
    <!-- 固定宽度的 pin slot 避免打钉后虚拟列表行宽变化造成视觉抖动。 -->
    <span class="pin-slot" data-test="log-pin-slot">
      <LogPinBadge
        v-if="evidencePin"
        :pin="evidencePin"
        @edit="(pin, event) => emit('edit-pin', pin, event)"
      />
    </span>
    <span class="ts">{{ time }}</span>
    <span class="svc" :style="{ color: serviceColor(serviceName) }">[{{ serviceName }}]</span>
    <span class="level" :style="{ color: levelColor }">{{ log.level.padEnd(5) }}</span>
    <SelectableLogText :text="log.message" @selection-change="(t, r) => emit('selection-change', t, r)" />
    <span v-if="repeatCount > 1" class="repeat-badge">×{{ repeatCount }}</span>
  </div>
</template>

<style scoped>
.log-row {
  display: grid;
  grid-template-columns: 30px 76px minmax(98px, 150px) 58px minmax(0, 1fr) auto;
  align-items: start;
  column-gap: 8px;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  line-height: 1.58;
  white-space: pre-wrap;
  word-break: break-word;
}
:deep(.selectable-msg) {
  min-width: 0;
  overflow-wrap: anywhere;
}
.log-row.has-pin {
  box-shadow: inset 2px 0 0 rgba(88, 166, 255, 0.45);
}
.log-row.is-time-anchor {
  outline: 1px solid rgba(54, 207, 201, 0.22);
}
.pin-slot {
  width: 30px;
  min-width: 30px;
  min-height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
}
.ts {
  color: var(--text-tertiary);
}
.svc {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.level {
  width: auto;
  font-weight: 650;
}
.repeat-badge {
  font-size: 10px;
  color: var(--text-tertiary);
  padding: 0 4px;
  border-radius: 3px;
  background: rgba(255, 255, 255, 0.06);
}

@container (max-width: 520px) {
  .log-row {
    grid-template-columns: 30px 70px 50px minmax(0, 1fr) auto;
    column-gap: 6px;
    padding: 2px 6px;
    font-size: 10.5px;
  }

  .svc {
    display: none;
  }
}

@container (max-width: 380px) {
  .log-row {
    grid-template-columns: 28px 62px 42px minmax(0, 1fr) auto;
    column-gap: 5px;
    font-size: 10px;
  }

  .pin-slot {
    width: 28px;
    min-width: 28px;
  }
}
</style>
