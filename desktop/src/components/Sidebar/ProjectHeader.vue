<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import type { Project } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ project: Project; projects: Project[] }>()
const emit = defineEmits<{
  'add-project': []
  'select-project': [projectId: string]
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
    <div class="project-picker">
      <button
        type="button"
        class="project-selector"
        data-test="project-selector"
        :aria-label="t('shell.sidebar.switchProject')"
        :aria-expanded="menuOpen"
        @click="menuOpen = !menuOpen"
      >
        <span class="project-mark" aria-hidden="true"></span>
        <span class="project-name">{{ project.name }}</span>
        <span class="selector-chevron" aria-hidden="true">{{ menuOpen ? '▴' : '▾' }}</span>
      </button>
      <div v-if="menuOpen" class="project-menu" data-test="project-menu">
        <button
          v-for="item in projects"
          :key="item.id"
          type="button"
          class="project-menu-item project-option"
          :class="{ selected: isSelected(item.id) }"
          :data-test="`project-option-${item.id}`"
          @click="selectProject(item.id)"
        >
          <span class="menu-item-icon project-dot" aria-hidden="true"></span>
          <span class="project-option-name">{{ item.name }}</span>
          <span v-if="isSelected(item.id)" class="selected-label">{{ t('shell.sidebar.currentProject') }}</span>
        </button>
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
</template>

<style scoped>
.project-header {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 12px 10px;
}

.project-picker {
  position: relative;
  min-width: 0;
  flex: 1;
}

.project-selector {
  display: grid;
  width: 100%;
  height: 38px;
  grid-template-columns: 18px minmax(0, 1fr) 14px;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border: 1px solid rgba(91, 106, 128, 0.45);
  border-radius: 6px;
  background: rgba(15, 23, 32, 0.82);
  color: var(--text-primary);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.14s ease, background 0.14s ease;
}

.project-selector:hover,
.project-selector[aria-expanded="true"] {
  border-color: rgba(88, 166, 255, 0.58);
  background: rgba(20, 31, 43, 0.96);
}

.project-mark {
  position: relative;
  width: 16px;
  height: 16px;
  border: 1px solid rgba(139, 148, 158, 0.72);
  border-radius: 5px;
}

.project-mark::after {
  position: absolute;
  inset: 4px;
  content: '';
  border-radius: 2px;
  background: #3fb950;
}

.project-name {
  min-width: 0;
  overflow: hidden;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 700;
  text-overflow: ellipsis;
  text-transform: uppercase;
  white-space: nowrap;
}

.selector-chevron {
  color: var(--text-secondary);
  font-size: 10px;
  text-align: right;
}

.project-menu {
  position: absolute;
  z-index: 40;
  top: 44px;
  left: 0;
  right: 0;
  padding: 6px;
  border: 1px solid rgba(91, 106, 128, 0.45);
  border-radius: 7px;
  background: #0f1720;
  box-shadow: 0 18px 44px rgba(0, 0, 0, 0.32);
}

.project-menu-item {
  display: grid;
  width: 100%;
  min-height: 32px;
  grid-template-columns: 16px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  text-align: left;
}

.project-menu-item:hover,
.project-menu-item.selected {
  background: rgba(88, 166, 255, 0.12);
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

.project-option-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.selected-label {
  color: var(--accent);
  font-size: 10px;
}

.add-project-btn {
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  border: 1px solid rgba(91, 106, 128, 0.45);
  border-radius: 6px;
  background: rgba(22, 31, 42, 0.86);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 22px;
  line-height: 1;
}

.add-project-btn:hover {
  border-color: rgba(88, 166, 255, 0.58);
  color: var(--text-primary);
}
</style>
