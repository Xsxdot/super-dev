<script setup lang="ts">
import { onMounted } from 'vue'
import { useAgentStore } from '@/stores/agent'
import SidebarView from '@/components/Sidebar/SidebarView.vue'

const agentStore = useAgentStore()

onMounted(() => {
  agentStore.startPolling()
})
</script>

<template>
  <div class="flex h-screen overflow-hidden" style="background: var(--bg-primary)">
    <SidebarView />
    <div class="flex flex-col flex-1 overflow-hidden" style="color: var(--text-secondary); padding: 20px;">
      面板区（Task 7）
    </div>
  </div>
</template>
