<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import WorkspaceTabs from '@/components/Workspace/WorkspaceTabs.vue'
import BottomBar from '@/components/BottomBar.vue'

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
agentStore.startPolling()

const showWorkspaceTabs = computed(() =>
  workspace.tabs.length > 0 && !workspace.isRuntimeWorkspaceMaximized,
)
</script>

<template>
  <div class="main-layout" data-test="main-layout">
    <div v-if="showWorkspaceTabs" class="app-topbar" data-test="app-topbar">
      <WorkspaceTabs />
    </div>
    <div class="main-content-row" data-test="main-content-row">
      <SidebarView v-if="!workspace.isRuntimeWorkspaceMaximized" />
      <div class="workspace-column" :class="{ 'with-app-tabs': showWorkspaceTabs }">
        <WorkspaceShell />
      </div>
    </div>
    <BottomBar v-if="!workspace.isRuntimeWorkspaceMaximized" />
  </div>
</template>

<style scoped>
.main-layout {
  --sidebar-width: 280px;
  display: flex;
  position: relative;
  height: 100vh;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-primary);
}

.app-topbar {
  position: absolute;
  z-index: 30;
  top: 0;
  right: 0;
  left: var(--sidebar-width);
  height: 34px;
  border-bottom: 1px solid var(--border-secondary);
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

.workspace-column.with-app-tabs {
  padding-top: 34px;
}
</style>
