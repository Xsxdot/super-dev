<script setup lang="ts">
import type { Project } from '@/api/agent'

defineProps<{ project: Project }>()
</script>

<template>
  <div class="project-header">
    <span class="project-name">{{ project.name }}</span>
  </div>
</template>

<style scoped>
.project-header {
  padding: 10px 8px 4px 10px;
}
.project-name {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}
</style>
