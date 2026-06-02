<script setup lang="ts">
import { useRouter } from 'vue-router'
import type { Project } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ project: Project }>()
const router = useRouter()
const { t } = useAppI18n()

function openOverview() {
  void router.push(`/project/${encodeURIComponent(props.project.id)}/overview`)
}
</script>

<template>
  <div class="project-header">
    <span class="project-name">{{ project.name }}</span>
    <button
      type="button"
      class="overview-btn"
      data-test="project-overview"
      :aria-label="t('overview.openOverviewForProject', { name: project.name })"
      :title="t('overview.openOverview')"
      @click="openOverview"
    />
  </div>
</template>

<style scoped>
.project-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 10px 8px 4px 10px;
}
.project-name {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-overflow: ellipsis;
  text-transform: uppercase;
  white-space: nowrap;
}
.overview-btn {
  position: relative;
  width: 22px;
  height: 22px;
  flex: 0 0 22px;
  border: 0;
  background: transparent;
  color: var(--text-tertiary);
  cursor: pointer;
}
.overview-btn::before,
.overview-btn::after {
  position: absolute;
  content: '';
}
.overview-btn::before {
  inset: 6px;
  border: 1.5px solid currentColor;
  border-radius: 50%;
}
.overview-btn::after {
  top: 10px;
  left: 10px;
  width: 2px;
  height: 2px;
  border-radius: 50%;
  background: currentColor;
}
.overview-btn:hover {
  color: var(--text-primary);
}
</style>
