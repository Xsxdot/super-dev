<script setup lang="ts">
import type { Project } from '@/api/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ project: Project }>()
defineEmits<{ 'add-project': [] }>()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()

function openOverview() {
  workspace.openProjectOverview(props.project.id)
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
    <button
      type="button"
      class="add-project-btn"
      data-test="sidebar-add-project"
      :aria-label="t('shell.sidebar.addProject')"
      :title="t('shell.sidebar.addProject')"
      @click="$emit('add-project')"
    >
      +
    </button>
  </div>
</template>

<style scoped>
.project-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 10px 10px 6px;
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
.add-project-btn {
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 18px;
  line-height: 1;
}
.add-project-btn:hover {
  border-color: var(--border);
  color: var(--text-primary);
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
