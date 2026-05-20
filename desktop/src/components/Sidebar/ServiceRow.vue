<script setup lang="ts">
import { ref } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Service } from '@/api/agent'

const props = defineProps<{
  service: Service
  selected: boolean
}>()

const emit = defineEmits<{
  click: []
  dragstart: [serviceId: string]
}>()

const agentStore = useAgentStore()
const hovered = ref(false)

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

function onDragStart(e: DragEvent) {
  e.dataTransfer?.setData('text/plain', props.service.id)
  emit('dragstart', props.service.id)
}
</script>

<template>
  <div
    class="service-row"
    :class="{ selected }"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @click="emit('click')"
    draggable="true"
    @dragstart="onDragStart"
  >
    <input
      type="checkbox"
      :checked="service.required || undefined"
      :disabled="service.required"
      @click.stop
      @change.stop
      class="service-checkbox"
    />
    <span class="status-dot" :style="{ background: statusColor(service.status) }" />
    <span class="service-name">{{ service.name }}</span>

    <Transition name="fade">
      <div v-if="hovered" class="hover-actions" @click.stop>
        <template v-if="service.status === 'running' || service.status === 'starting'">
          <button title="重启" @click="agentStore.restartService(service.id)">↺</button>
          <button title="停止" class="stop-btn" @click="agentStore.stopService(service.id)">⏹</button>
        </template>
        <template v-else>
          <button title="启动" class="start-btn" @click="agentStore.startService(service.id)">▶</button>
        </template>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.service-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px 3px 10px;
  border-radius: 4px;
  margin: 1px 4px;
  cursor: pointer;
  transition: background 0.12s;
}
.service-row:hover { background: rgba(255,255,255,0.04); }
.service-row.selected { background: rgba(31,111,235,0.12); }

.service-checkbox {
  width: 12px; height: 12px;
  accent-color: #1f6feb;
  flex-shrink: 0;
  cursor: pointer;
}

.status-dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.service-name {
  flex: 1;
  font-size: 12px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hover-actions {
  display: flex;
  gap: 3px;
  align-items: center;
  background: linear-gradient(to right, transparent, var(--bg-elevated) 40%);
  padding-left: 16px;
  position: absolute;
  right: 6px;
}
.hover-actions button {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 3px;
  padding: 1px 5px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
}
.hover-actions .stop-btn {
  background: rgba(248,81,73,0.1);
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}
.hover-actions .start-btn {
  background: rgba(63,185,80,0.1);
  border-color: rgba(63,185,80,0.3);
  color: #3fb950;
}

.fade-enter-active, .fade-leave-active { transition: opacity 0.15s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
