<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import BottomBar from '@/components/BottomBar.vue'

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
agentStore.startPolling()
</script>

<template>
  <div class="main-layout" data-test="main-layout">
    <div class="main-content-row" data-test="main-content-row">
      <SidebarView v-if="!workspace.isRuntimeWorkspaceMaximized" />
      <div class="workspace-column">
        <WorkspaceShell />
      </div>
    </div>
    <BottomBar v-if="!workspace.isRuntimeWorkspaceMaximized" />
  </div>
</template>

<style scoped>
.main-layout {
  display: flex;
  height: 100vh;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-primary);
}

.main-content-row {
  display: flex;
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.workspace-column {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
}
</style>
