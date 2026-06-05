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

const showAppTopbar = computed(() => !workspace.isRuntimeWorkspaceMaximized)
const showWorkspaceTabs = computed(() =>
  workspace.tabs.length > 0 && !workspace.isRuntimeWorkspaceMaximized,
)
</script>

<template>
  <div class="main-layout" data-test="main-layout">
    <div
      v-if="showAppTopbar"
      class="app-topbar"
      data-test="app-topbar"
      data-tauri-drag-region
    >
      <div class="app-brand" data-test="app-brand" data-tauri-drag-region>
        <span class="app-brand-title" data-tauri-drag-region>SuperDev</span>
      </div>
      <div class="app-tabs-region" data-test="app-tabs-region">
        <WorkspaceTabs v-if="showWorkspaceTabs" />
        <div class="app-drag-fill" data-tauri-drag-region />
      </div>
    </div>
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
  --sidebar-width: 280px;
  --app-topbar-height: 52px;
  display: flex;
  position: relative;
  height: 100vh;
  flex-direction: column;
  overflow: hidden;
  background: var(--bg-primary);
}

.app-topbar {
  z-index: 30;
  display: flex;
  height: var(--app-topbar-height);
  flex-shrink: 0;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-primary);
}

.app-brand {
  display: flex;
  width: var(--sidebar-width);
  height: 100%;
  flex-shrink: 0;
  align-items: center;
  padding: 0 18px 0 108px;
  border-right: 1px solid var(--border-secondary);
}

.app-brand-title {
  overflow: hidden;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 700;
  line-height: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-tabs-region {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: stretch;
  overflow: hidden;
}

.app-tabs-region :deep(.workspace-tabs) {
  height: 100%;
  min-width: 0;
  max-width: 100%;
  flex: 0 1 auto;
  align-items: flex-end;
  padding: 8px 8px 0;
  border-bottom: 0;
  background: transparent;
}

.app-tabs-region :deep(.workspace-tab) {
  height: 44px;
  padding: 0 14px;
  font-size: 13px;
}

.app-drag-fill {
  min-width: 80px;
  flex: 1;
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
