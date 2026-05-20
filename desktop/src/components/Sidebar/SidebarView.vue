<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { usePanelStore } from '@/stores/panel'
import ProjectHeader from './ProjectHeader.vue'
import ServiceRow from './ServiceRow.vue'
import { open, message } from '@tauri-apps/plugin-dialog'

const agentStore = useAgentStore()
const panelStore = usePanelStore()

const focusedLeaf = computed(() =>
  panelStore.allLeaves.find(l => l.id === panelStore.focusedPanelId)
)

function isServiceSelected(serviceId: string) {
  return focusedLeaf.value?.serviceId === serviceId
}

function selectService(serviceId: string, projectId: string) {
  const panelId = panelStore.targetPanelId()
  if (!panelId) return
  panelStore.replaceScope(panelId, serviceId, projectId)
  panelStore.setFocus(panelId)
}

async function addProject() {
  const selected = await open({ directory: true, multiple: false, title: '选择项目根目录' })
  if (!selected || Array.isArray(selected)) return
  try {
    await agentStore.addProject(selected)
  } catch (e) {
    const msg = e instanceof Error ? e.message : '添加项目失败'
    await message(
      msg.includes('config') ? `${msg}\n请确认目录中有 .superdev/config.yaml` : msg,
      { title: '无法添加项目', kind: 'error' },
    )
  }
}
</script>

<template>
  <div class="sidebar">
    <div class="sidebar-scroll">
      <template v-for="project in agentStore.projects" :key="project.id">
        <ProjectHeader :project="project" />
        <ServiceRow
          v-for="service in project.services"
          :key="service.id"
          :service="service"
          :project-id="project.id"
          :selected="isServiceSelected(service.id)"
          @click="selectService(service.id, project.id)"
        />
      </template>
    </div>
    <div class="add-project" @click="addProject">+ 添加项目</div>
  </div>
</template>

<style scoped>
.sidebar {
  width: 185px;
  min-width: 160px;
  max-width: 200px;
  background: var(--bg-primary);
  border-right: 1px solid var(--border-secondary);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  overflow: hidden;
}
.sidebar-scroll {
  flex: 1;
  overflow-y: auto;
  padding-bottom: 8px;
}
.add-project {
  padding: 8px 12px;
  border-top: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 11px;
  cursor: pointer;
  transition: color 0.12s;
}
.add-project:hover { color: var(--text-secondary); }
</style>
