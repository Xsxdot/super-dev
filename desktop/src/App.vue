<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import SidebarView from '@/components/Sidebar/SidebarView.vue'
import PanelLayout from '@/components/Panel/PanelLayout.vue'

const agentStore = useAgentStore()
agentStore.startPolling()
</script>

<template>
  <div class="flex h-screen overflow-hidden" style="background: var(--bg-primary)">
    <SidebarView />
    <div class="flex flex-col flex-1 overflow-hidden">
      <PanelLayout />
      <div style="background: var(--bg-elevated); border-top: 1px solid var(--border); padding: 4px 12px; color: var(--text-tertiary); font-size: 10px; flex-shrink: 0;">
        BottomBar（Task 9）
      </div>
    </div>
  </div>
</template>
