<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import BottomBar from '@/components/BottomBar.vue'

const agentStore = useAgentStore()
agentStore.startPolling()
</script>

<template>
  <div class="flex h-screen overflow-hidden" style="background: var(--bg-primary)">
    <SidebarView />
    <div class="flex flex-col flex-1 overflow-hidden">
      <WorkspaceShell />
      <BottomBar />
    </div>
  </div>
</template>
