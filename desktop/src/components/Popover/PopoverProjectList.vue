<script setup lang="ts">
import { ref } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'

const emit = defineEmits<{
  hover: [project: Project | null]
}>()

const agentStore = useAgentStore()
const searchText = ref('')
const hoveredProjectId = ref<string | null>(null)

function filteredServices(project: Project) {
  if (!searchText.value) return project.services
  return project.services.filter(s =>
    s.name.toLowerCase().includes(searchText.value.toLowerCase())
  )
}

function projectStatusColor(project: Project) {
  const services = project.services
  if (services.some(s => s.status === 'failed')) return '#f85149'
  if (services.some(s => s.status === 'running')) return '#3fb950'
  if (services.some(s => s.status === 'starting')) return '#d29922'
  return '#6e7681'
}

function serviceStatusColor(status: string) {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

function onProjectHover(project: Project) {
  hoveredProjectId.value = project.id
  emit('hover', project)
}

function onProjectLeave() {
  hoveredProjectId.value = null
  emit('hover', null)
}
</script>

<template>
  <div class="project-list">
    <!-- 搜索栏 -->
    <div class="search-bar">
      <span class="search-icon">⌕</span>
      <input
        v-model="searchText"
        placeholder="搜索服务…"
        class="search-input"
      />
    </div>

    <div class="divider" />

    <!-- 项目列表 -->
    <div class="list-scroll">
      <template v-for="project in agentStore.projects" :key="project.id">
        <div
          v-if="filteredServices(project).length > 0"
          class="project-section"
          @mouseenter="onProjectHover(project)"
          @mouseleave="onProjectLeave"
        >
          <!-- 项目 label -->
          <div class="project-label">
            <span class="project-name">{{ project.name.toUpperCase() }}</span>
            <span
              class="project-dot"
              :style="{ background: projectStatusColor(project) }"
            />
          </div>
          <!-- 服务行（左栏简化版：只有状态点 + 名称） -->
          <div
            v-for="svc in filteredServices(project)"
            :key="svc.id"
            class="left-service-row"
            :class="{ 'row-hovered': hoveredProjectId === project.id }"
          >
            <span
              class="status-dot"
              :style="{ background: serviceStatusColor(svc.status) }"
            />
            <span class="svc-name">{{ svc.name }}</span>
          </div>
        </div>
      </template>
    </div>

    <div class="divider" />

    <!-- 未连接提示 -->
    <div v-if="!agentStore.connected" class="disconnected">未连接</div>
  </div>
</template>

<style scoped>
.project-list {
  width: 170px;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  flex-shrink: 0;
}

.search-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 10px;
  padding: 5px 9px;
  background: var(--bg-elevated);
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
}
.search-icon { font-size: 12px; color: var(--text-tertiary); }
.search-input {
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  font-size: 10px;
  color: var(--text-secondary);
}
.search-input::placeholder { color: var(--text-tertiary); }

.divider { height: 1px; background: var(--border); flex-shrink: 0; }

.list-scroll { flex: 1; overflow-y: auto; }

.project-section { cursor: default; }

.project-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px 3px;
}
.project-name {
  font-size: 9px;
  font-weight: 600;
  color: var(--text-tertiary);
  letter-spacing: 0.08em;
}
.project-dot {
  width: 6px; height: 6px;
  border-radius: 50%;
}

.left-service-row {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 5px 10px;
  border-left: 2px solid transparent;
}
.left-service-row.row-hovered {
  background: var(--bg-elevated);
  border-left-color: var(--accent);
}

.status-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.svc-name { font-size: 11px; color: var(--text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-hovered .svc-name { color: var(--text-primary); }

.disconnected {
  padding: 8px 10px;
  font-size: 10px;
  color: var(--text-tertiary);
  text-align: center;
}
</style>
