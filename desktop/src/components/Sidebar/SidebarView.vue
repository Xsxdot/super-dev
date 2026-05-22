<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import { usePanelStore } from '@/stores/panel'
import { useWorkspaceStore } from '@/stores/workspace'
import ProjectHeader from './ProjectHeader.vue'
import ServiceRow from './ServiceRow.vue'
import RemoteListenSection from './RemoteListenSection.vue'
import ProjectRemoteSection from './ProjectRemoteSection.vue'
import { open, message } from '@tauri-apps/plugin-dialog'
import { useRouter } from 'vue-router'

const agentStore = useAgentStore()
const panelStore = usePanelStore()
const workspace = useWorkspaceStore()
const router = useRouter()

function isServiceSelected(serviceId: string) {
  const active = workspace.activeTab
  if (!active || active.type !== 'project') return false
  return panelStore.allLeaves.some(leaf => leaf.serviceId === serviceId)
}

function selectService(serviceId: string, projectId: string) {
  workspace.openService(projectId, serviceId)
}

function openProjectSearch(projectId: string) {
  workspace.openSearch(projectId)
}

function openRemoteGroup(payload: { logSourceId: string; groupKey: string }) {
  workspace.openRemote(payload.logSourceId, payload.groupKey)
}

function openRemoteAggregate(payload: {
  projectId: string
  serviceId: string
  serviceName: string
  logSourceIds: string[]
  groupKey: string
}) {
  workspace.openRemoteAggregate(
    payload.projectId,
    payload.serviceId,
    payload.serviceName,
    payload.logSourceIds,
    payload.groupKey,
  )
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
        <ProjectHeader :project="project" @search="openProjectSearch(project.id)" />
        <ServiceRow
          v-for="service in project.services"
          :key="service.id"
          :service="service"
          :project-id="project.id"
          :selected="isServiceSelected(service.id)"
          @click="selectService(service.id, project.id)"
        />
        <ProjectRemoteSection
          :project-id="project.id"
          @open="openRemoteAggregate"
        />
      </template>
      <RemoteListenSection @open="openRemoteGroup" />
    </div>
    <div class="settings-entry" @click="router.push('/settings')">⚙ 设置</div>
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
.settings-entry {
  padding: 8px 12px;
  border-top: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 11px;
  cursor: pointer;
  transition: color 0.12s;
}
.settings-entry:hover { color: var(--text-secondary); }
</style>
