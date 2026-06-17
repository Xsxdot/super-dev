<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import type { Project } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  project: Project
  projects: Project[]
  visibleProjects: Project[]
  pinnedProjectIds: string[]
}>()
const emit = defineEmits<{
  'add-project': []
  'select-project': [projectId: string]
  'toggle-pin': [projectId: string]
}>()
const { t } = useAppI18n()
const menuOpen = ref(false)
const rootEl = ref<HTMLElement | null>(null)

function selectProject(projectId: string) {
  emit('select-project', projectId)
  menuOpen.value = false
}

function isSelected(projectId: string): boolean {
  return projectId === props.project.id
}

function isPinned(projectId: string): boolean {
  return props.pinnedProjectIds.includes(projectId)
}

function togglePin(projectId: string) {
  emit('toggle-pin', projectId)
}

function projectInitials(name: string): string {
  const parts = name
    .trim()
    .split(/\s+/)
    .filter(Boolean)
  if (parts.length >= 2) {
    return parts.slice(0, 2).map(part => part[0]?.toUpperCase() ?? '').join('')
  }
  return (parts[0] ?? name).slice(0, 2).toUpperCase()
}

function closeOnOutsidePointer(event: PointerEvent) {
  if (!menuOpen.value) return
  if (rootEl.value?.contains(event.target as Node)) return
  menuOpen.value = false
}

onMounted(() => {
  document.addEventListener('pointerdown', closeOnOutsidePointer)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', closeOnOutsidePointer)
})
</script>

<template>
  <div ref="rootEl" class="project-header">
    <div class="project-chip-row" data-test="project-chips-row">
      <button
        v-for="item in visibleProjects"
        :key="item.id"
        type="button"
        class="project-chip"
        :class="{ selected: isSelected(item.id), pinned: isPinned(item.id) }"
        :data-test="`project-chip-${item.id}`"
        :title="item.name"
        @click="selectProject(item.id)"
      >
        <span class="project-chip-dot" aria-hidden="true"></span>
        <span v-if="isPinned(item.id)" class="project-chip-pin" aria-hidden="true">★</span>
        <span class="project-chip-label">{{ projectInitials(item.name) }}</span>
      </button>
      <div class="project-more-wrap">
        <button
          type="button"
          class="project-more-btn"
          data-test="project-more"
          :aria-label="t('shell.sidebar.moreProjects')"
          :aria-expanded="menuOpen"
          @click="menuOpen = !menuOpen"
        >
          {{ t('shell.sidebar.moreProjects') }}
        </button>
        <div v-if="menuOpen" class="project-menu" data-test="project-menu">
          <div
            v-for="item in projects"
            :key="item.id"
            class="project-menu-row"
            :class="{ selected: isSelected(item.id) }"
          >
            <button
              type="button"
              class="project-menu-item project-option"
              :data-test="`project-option-${item.id}`"
              @click="selectProject(item.id)"
            >
              <span class="menu-item-icon project-dot" aria-hidden="true"></span>
              <span class="project-option-main">
                <span class="project-option-name">{{ item.name }}</span>
                <span class="project-option-path">{{ item.root_path }}</span>
              </span>
              <span v-if="isSelected(item.id)" class="selected-label">{{ t('shell.sidebar.currentProject') }}</span>
            </button>
            <button
              type="button"
              class="pin-project-btn"
              :class="{ pinned: isPinned(item.id) }"
              :data-test="`project-pin-${item.id}`"
              :aria-label="isPinned(item.id) ? t('shell.sidebar.unpinProject') : t('shell.sidebar.pinProject')"
              :title="isPinned(item.id) ? t('shell.sidebar.unpinProject') : t('shell.sidebar.pinProject')"
              @click="togglePin(item.id)"
            >
              ★
            </button>
          </div>
        </div>
      </div>
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
    <div class="project-context" data-test="project-current-context">
      <span class="project-context-name">{{ project.name }}</span>
      <span class="project-context-path">{{ project.root_path }}</span>
    </div>
  </div>
</template>

<style scoped>
.project-header {
  position: relative;
  display: grid;
  gap: 7px;
  padding: 10px 10px 9px;
}

.project-chip-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
}

.project-more-wrap {
  position: relative;
  min-width: 0;
  flex: 0 0 auto;
}

.project-chip,
.project-more-btn,
.add-project-btn {
  display: inline-flex;
  height: 30px;
  align-items: center;
  justify-content: center;
  border: 1px solid rgba(91, 106, 128, 0.45);
  border-radius: 6px;
  background: rgba(15, 23, 32, 0.82);
  color: var(--text-primary);
  cursor: pointer;
  transition: border-color 0.14s ease, background 0.14s ease;
}

.project-chip {
  min-width: 44px;
  flex: 1 1 0;
  gap: 4px;
  padding: 0 7px;
  font-size: 12px;
  font-weight: 750;
}

.project-chip:hover,
.project-chip.selected,
.project-more-btn:hover,
.project-more-btn[aria-expanded="true"],
.add-project-btn:hover {
  border-color: rgba(88, 166, 255, 0.58);
  background: rgba(20, 31, 43, 0.96);
}

.project-chip.selected {
  border-color: rgba(88, 166, 255, 0.82);
  background: rgba(88, 166, 255, 0.16);
}

.project-chip-dot {
  width: 7px;
  height: 7px;
  flex: 0 0 7px;
  border-radius: 50%;
  background: #3fb950;
}

.project-chip-pin {
  color: #d29922;
  font-size: 10px;
  line-height: 1;
}

.project-chip-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.project-more-btn {
  width: 48px;
  padding: 0 8px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 650;
}

.add-project-btn {
  width: 32px;
  flex: 0 0 32px;
  padding: 0;
  color: var(--text-secondary);
  font-size: 19px;
  line-height: 1;
}

.project-menu {
  position: absolute;
  z-index: 40;
  top: 36px;
  left: auto;
  right: -38px;
  width: 248px;
  padding: 6px;
  border: 1px solid rgba(91, 106, 128, 0.45);
  border-radius: 7px;
  background: #0f1720;
  box-shadow: 0 18px 44px rgba(0, 0, 0, 0.32);
}

.project-menu-row {
  display: grid;
  width: 100%;
  min-height: 40px;
  grid-template-columns: minmax(0, 1fr) 28px;
  align-items: center;
  gap: 4px;
  border-radius: 5px;
}

.project-menu-row:hover,
.project-menu-row.selected {
  background: rgba(88, 166, 255, 0.12);
}

.project-menu-item {
  display: grid;
  min-width: 0;
  height: 100%;
  grid-template-columns: 16px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 0 6px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  text-align: left;
}

.project-menu-row:hover .project-menu-item,
.project-menu-row.selected .project-menu-item {
  color: var(--text-primary);
}

.menu-item-icon {
  width: 13px;
  height: 13px;
}

.project-dot {
  border-radius: 50%;
  background: #3fb950;
  box-shadow: 0 0 0 3px rgba(63, 185, 80, 0.13);
}

.project-option-main {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.project-option-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.project-option-path {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.selected-label {
  color: var(--accent);
  font-size: 10px;
}

.pin-project-btn {
  width: 26px;
  height: 26px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
}

.pin-project-btn:hover,
.pin-project-btn.pinned {
  background: rgba(210, 153, 34, 0.12);
  color: #d29922;
}

.project-context {
  display: grid;
  min-width: 0;
  gap: 2px;
  padding: 0 2px;
}

.project-context-name {
  min-width: 0;
  overflow: hidden;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 750;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.project-context-path {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
