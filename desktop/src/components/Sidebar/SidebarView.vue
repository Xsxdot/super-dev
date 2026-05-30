<script setup lang="ts">
import { useAgentStore } from '@/stores/agent'
import { usePanelStore } from '@/stores/panel'
import { useWorkspaceStore } from '@/stores/workspace'
import ProjectHeader from './ProjectHeader.vue'
import EnvGroup from './EnvGroup.vue'
import type { Service } from '@/api/agent'
import { open, message } from '@tauri-apps/plugin-dialog'
import { useRouter } from 'vue-router'

const agentStore = useAgentStore()
const panelStore = usePanelStore()
const workspace = useWorkspaceStore()
const router = useRouter()

function openDeployment(payload: { deploymentId: string; title: string }) {
  workspace.openDeployment(payload.deploymentId, payload.title)
}

function openProjectSearch(projectId: string) {
  workspace.openSearch(projectId)
}

function servicesForEnv(services: Service[], envName: string): Service[] {
  return services.filter(svc => svc.deployments?.some(d => d.env_name === envName))
}

/**
 * openDeploymentIdSet 返回所有面板（项目 tab 的分栏）中已打开的 deploymentId 集合，
 * 用于侧边栏行高亮。leaf.serviceId 语义即 deploymentId。
 */
function openDeploymentIdSet(): Set<string> {
  const active = workspace.activeTab
  if (!active || active.type !== 'project') return new Set()
  return new Set(panelStore.allLeaves.map(l => l.serviceId).filter(Boolean) as string[])
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
        <!-- 按环境分组展示有 deployment 的 service 行 -->
        <EnvGroup
          v-for="env in project.environments ?? []"
          :key="env.id || env.name"
          :env-name="env.name"
          :is-dev="env.is_dev"
          :project-id="project.id"
          :services="servicesForEnv(project.services, env.name)"
          :selected-service-ids="openDeploymentIdSet()"
          @open-deployment="openDeployment"
          @search="openProjectSearch(project.id)"
        />
      </template>
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
