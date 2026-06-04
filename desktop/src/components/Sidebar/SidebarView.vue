<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAgentStore } from '@/stores/agent'
import { usePanelStore } from '@/stores/panel'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAddProjectFlow } from '@/composables/useAddProjectFlow'
import ProjectHeader from './ProjectHeader.vue'
import EnvGroup from './EnvGroup.vue'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import type { Service } from '@/api/agent'
import { useRouter } from 'vue-router'

const agentStore = useAgentStore()
const panelStore = usePanelStore()
const workspace = useWorkspaceStore()
const router = useRouter()
const { t } = useI18n()
const {
  editorProject,
  editorIsNew,
  addProject,
  closeEditor,
  onEditorSaved,
} = useAddProjectFlow()

const serviceQuery = ref('')

function openDeployment(payload: { deploymentId: string; title: string }) {
  workspace.openDeployment(payload.deploymentId, payload.title)
}

function openProjectSearch(projectId: string) {
  workspace.openSearch(projectId)
}

function servicesForEnv(services: Service[], envName: string): Service[] {
  const query = serviceQuery.value.trim().toLowerCase()
  return services
    .filter(svc => svc.deployments?.some(d => d.env_name === envName))
    .filter(svc => !query || svc.name.toLowerCase().includes(query))
}

/**
 * openDeploymentIdSet 返回当前日志工作区已打开的 deploymentId 集合，
 * 让项目 tab 分栏和独立 deployment tab 都能正确高亮侧边栏行。
 */
function openDeploymentIdSet(): Set<string> {
  const active = workspace.activeTab
  if (!active || (active.type !== 'project' && active.type !== 'deployment')) return new Set()
  return new Set(panelStore.allLeaves.map(l => l.serviceId).filter(Boolean) as string[])
}
</script>

<template>
  <div class="sidebar">
    <div class="sidebar-scroll">
      <button
        v-if="agentStore.projects.length === 0"
        type="button"
        class="empty-add-project"
        data-test="sidebar-add-project"
        @click="addProject"
      >
        + {{ t('shell.sidebar.addProject') }}
      </button>
      <template v-for="project in agentStore.projects" :key="project.id">
        <ProjectHeader :project="project" @add-project="addProject" />
        <div class="sidebar-search">
          <span class="search-icon">⌕</span>
          <input
            v-model="serviceQuery"
            data-test="sidebar-service-search"
            :placeholder="t('shell.sidebar.searchServices')"
          />
        </div>
        <div class="drop-hint">
          <span class="drop-icon">▣</span>
          <span>{{ t('shell.sidebar.dragServiceToSplit') }}</span>
        </div>
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
    <button data-test="sidebar-settings" type="button" class="settings-entry" @click="router.push('/settings')">
      ⚙ {{ t('shell.sidebar.settings') }}
    </button>
  </div>
  <ProjectConfigEditor
    v-if="editorProject"
    :project="editorProject"
    :is-new="editorIsNew"
    @saved="onEditorSaved"
    @cancel="closeEditor"
  />
</template>

<style scoped>
.sidebar {
  width: 230px;
  min-width: 210px;
  max-width: 250px;
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
.empty-add-project {
  width: calc(100% - 20px);
  margin: 10px;
  height: 32px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
}
.empty-add-project:hover {
  border-color: var(--border);
  color: var(--text-primary);
}
.sidebar-search {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  margin: 0 10px 10px;
  padding: 0 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
}
.search-icon {
  color: var(--text-tertiary);
  font-size: 12px;
  flex-shrink: 0;
}
.sidebar-search input {
  min-width: 0;
  flex: 1;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text-primary);
  font-size: 12px;
}
.sidebar-search input::placeholder {
  color: var(--text-tertiary);
}
.drop-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  margin: 0 10px 12px;
  padding: 8px 10px;
  border: 1px dashed rgba(88, 166, 255, 0.45);
  border-radius: 6px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.drop-icon {
  color: #58a6ff;
  flex-shrink: 0;
}
.settings-entry {
  width: 100%;
  padding: 8px 12px;
  border-top: 1px solid var(--border-secondary);
  border-right: 0;
  border-bottom: 0;
  border-left: 0;
  background: transparent;
  text-align: left;
  color: var(--text-tertiary);
  font-size: 11px;
  cursor: pointer;
  transition: color 0.12s;
}
.settings-entry:hover { color: var(--text-secondary); }
</style>
