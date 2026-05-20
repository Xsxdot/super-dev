<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'
import PopoverProjectList from '@/components/Popover/PopoverProjectList.vue'
import PopoverServicePanel from '@/components/Popover/PopoverServicePanel.vue'

const agentStore = useAgentStore()
const hoveredProject = ref<Project | null>(null)

onMounted(() => agentStore.startPolling())
onUnmounted(() => agentStore.stopPolling())

function onProjectHover(project: Project | null) {
  if (project !== null) {
    hoveredProject.value = project
  }
  // null 由 popover-root @mouseleave 统一清除，防止移向右栏时闪烁
}
</script>

<template>
  <div
    class="popover-root"
    @mouseleave="hoveredProject = null"
  >
    <PopoverProjectList @hover="onProjectHover" />
    <div v-if="hoveredProject" class="panel-divider" />
    <PopoverServicePanel
      v-if="hoveredProject"
      :project="hoveredProject"
    />
  </div>
</template>

<style scoped>
.popover-root {
  display: flex;
  height: 100vh;
  background: var(--bg-primary);
  overflow: hidden;
}
.panel-divider {
  width: 1px;
  background: var(--border);
  flex-shrink: 0;
}
</style>
