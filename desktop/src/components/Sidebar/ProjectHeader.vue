<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'

const props = defineProps<{ project: Project }>()
const agentStore = useAgentStore()

const canStartSelected = computed(() =>
  props.project.services
    .filter(s => agentStore.isServiceSelectedForStart(props.project.id, s.name))
    .some(s => s.status !== 'running' && s.status !== 'starting')
)

async function startSelected() {
  if (!canStartSelected.value) return
  await agentStore.startSelected(props.project.id)
}

async function stopAll() {
  const running = props.project.services.filter(
    s => s.status === 'running' || s.status === 'starting'
  )
  await Promise.all(running.map(s => agentStore.stopService(s.id)))
}
</script>

<template>
  <div class="project-header">
    <span class="project-name">{{ project.name }}</span>
    <div class="project-actions">
      <button title="启动选中" class="action-btn start" :disabled="!canStartSelected" @click.stop="startSelected">▶</button>
      <button title="全部停止" class="action-btn stop" @click.stop="stopAll">⏹</button>
    </div>
  </div>
</template>

<style scoped>
.project-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 8px 4px 10px;
}
.project-name {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}
.project-actions { display: flex; gap: 4px; }
.action-btn {
  background: transparent;
  border: none;
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 12px;
  cursor: pointer;
  transition: background 0.12s;
}
.action-btn:hover:not(:disabled) { background: rgba(255,255,255,0.08); }
.action-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.action-btn.start { color: #3fb950; }
.action-btn.stop { color: var(--text-secondary); }
</style>
