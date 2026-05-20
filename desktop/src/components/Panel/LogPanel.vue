<script setup lang="ts">
defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
}>()
</script>

<template>
  <div class="log-panel-placeholder" style="flex: 1; background: var(--bg-primary); display: flex; align-items: center; justify-content: center; color: var(--text-tertiary); font-size: 12px;">
    {{ serviceId ? `日志: ${serviceId}` : '选择服务查看日志' }}
  </div>
</template>
