<script setup lang="ts">
import { onMounted } from 'vue'
import { useAgentStore } from '@/stores/agent'

const agentStore = useAgentStore()

onMounted(() => {
  agentStore.startPolling()
})
</script>

<template>
  <div class="flex h-screen overflow-hidden" style="background: var(--bg-primary); color: var(--text-secondary); padding: 20px;">
    SuperDev 加载中…
  </div>
</template>
