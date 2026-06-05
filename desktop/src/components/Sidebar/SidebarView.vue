<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
const searchInput = ref<HTMLInputElement | null>(null)
const selectedProjectId = ref<string | null>(null)
const selectedProject = computed(() =>
  agentStore.projects.find(project => project.id === selectedProjectId.value)
  ?? agentStore.projects[0]
  ?? null,
)

watch(
  () => agentStore.projects.map(project => project.id),
  (projectIds) => {
    if (projectIds.length === 0) {
      selectedProjectId.value = null
      return
    }
    if (!selectedProjectId.value || !projectIds.includes(selectedProjectId.value)) {
      selectedProjectId.value = projectIds[0]
    }
  },
  { immediate: true },
)

function openDeployment(payload: { deploymentId: string; title: string }) {
  workspace.openDeployment(payload.deploymentId, payload.title)
}

function selectProject(projectId: string) {
  selectedProjectId.value = projectId
  serviceQuery.value = ''
}

function openProjectSearch(projectId: string) {
  workspace.openSearch(projectId)
}

function openProjectOverview(projectId: string) {
  workspace.openProjectOverview(projectId)
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

function onGlobalKeydown(event: KeyboardEvent) {
  if (!(event.metaKey || event.ctrlKey)) return
  if (event.key.toLowerCase() !== 'k') return
  event.preventDefault()
  searchInput.value?.focus()
}

onMounted(() => {
  document.addEventListener('keydown', onGlobalKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onGlobalKeydown)
})
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
      <template v-if="selectedProject">
        <ProjectHeader
          :project="selectedProject"
          :projects="agentStore.projects"
          @select-project="selectProject"
          @add-project="addProject"
        />
        <div class="sidebar-project-shell" data-test="sidebar-project-shell">
          <div class="sidebar-search">
            <span class="search-icon">⌕</span>
            <input
              ref="searchInput"
              v-model="serviceQuery"
              data-test="sidebar-service-search"
              :placeholder="t('shell.sidebar.searchServices')"
            />
            <span class="search-shortcut" data-test="sidebar-search-shortcut">⌘K</span>
          </div>
          <button
            type="button"
            class="project-overview-strip"
            data-test="project-overview"
            @click="openProjectOverview(selectedProject.id)"
          >
            <span class="overview-strip-icon" aria-hidden="true"></span>
            <span class="overview-strip-main">{{ t('overview.openOverview') }}</span>
            <span class="overview-strip-hint">{{ t('shell.sidebar.projectOverviewHint') }}</span>
          </button>
          <div class="drop-hint" data-test="sidebar-drop-hint">
            <span class="drop-icon">▣</span>
            <span>{{ t('shell.sidebar.dragServiceToSplit') }}</span>
          </div>
          <!-- 按环境分组展示有 deployment 的 service 行 -->
          <EnvGroup
            v-for="(env, index) in selectedProject.environments ?? []"
            :key="env.id || env.name"
            :env-name="env.name"
            :is-dev="env.is_dev"
            :initially-expanded="env.is_dev || index === 0"
            :project-id="selectedProject.id"
            :services="servicesForEnv(selectedProject.services, env.name)"
            :selected-service-ids="openDeploymentIdSet()"
            @open-deployment="openDeployment"
            @search="openProjectSearch(selectedProject.id)"
          />
        </div>
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
  width: 280px;
  min-width: 280px;
  max-width: 280px;
  background:
    linear-gradient(180deg, rgba(12, 25, 34, 0.98), rgba(8, 13, 20, 0.98)),
    var(--bg-primary);
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

.sidebar-project-shell {
  padding: 0 10px 12px;
}

.sidebar-search {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 36px;
  margin: 0 2px 12px;
  padding: 0 9px;
  border: 1px solid rgba(91, 106, 128, 0.42);
  border-radius: 6px;
  background: rgba(13, 20, 29, 0.78);
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

.search-shortcut {
  padding: 0 2px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}

.project-overview-strip {
  display: grid;
  width: calc(100% - 4px);
  min-height: 42px;
  grid-template-columns: 20px minmax(0, auto) minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  margin: 0 2px 10px;
  padding: 0 11px;
  border: 1px solid rgba(91, 106, 128, 0.32);
  border-radius: 7px;
  background: rgba(15, 24, 34, 0.62);
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.12s, background 0.12s, color 0.12s;
}

.project-overview-strip:hover {
  border-color: rgba(88, 166, 255, 0.42);
  background: rgba(24, 39, 54, 0.72);
  color: var(--text-primary);
}

.overview-strip-icon {
  width: 16px;
  height: 16px;
  border: 2px solid currentColor;
  border-radius: 50%;
  opacity: 0.92;
}

.overview-strip-main {
  min-width: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
  white-space: nowrap;
}

.overview-strip-hint {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.drop-hint {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 46px;
  margin: 0 2px 12px;
  padding: 0 10px;
  border: 1px dashed rgba(88, 166, 255, 0.34);
  border-radius: 7px;
  background: rgba(88, 166, 255, 0.055);
  color: var(--text-secondary);
  font-size: 12px;
}
.drop-icon {
  color: #58a6ff;
  opacity: 0.8;
  flex-shrink: 0;
}
.settings-entry {
  width: 100%;
  padding: 12px 18px;
  border-top: 1px solid var(--border-secondary);
  border-right: 0;
  border-bottom: 0;
  border-left: 0;
  background: transparent;
  text-align: left;
  color: var(--text-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: color 0.12s;
}
.settings-entry:hover { color: var(--text-secondary); }
</style>
