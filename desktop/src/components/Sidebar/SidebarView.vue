<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import {
  COMPLETED_STEPS_KEY,
  DISMISSED_KEY,
  deriveDetection,
  isSampleProject,
  useGettingStartedStore,
} from '@/stores/gettingStarted'
import { useNodeStore } from '@/stores/node'
import { usePanelStore } from '@/stores/panel'
import { useSettingsStore } from '@/stores/settings'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAddProjectFlow } from '@/composables/useAddProjectFlow'
import ProjectHeader from './ProjectHeader.vue'
import EnvGroup from './EnvGroup.vue'
import GettingStartedEntry from './GettingStartedEntry.vue'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import type { Service } from '@/api/agent'
import { useRouter } from 'vue-router'

const agentStore = useAgentStore()
const gettingStarted = useGettingStartedStore()
const nodeStore = useNodeStore()
const panelStore = usePanelStore()
const settingsStore = useSettingsStore()
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
const PROJECT_RECENT_KEY = 'superdev.sidebar_project_recent.v1'
const PROJECT_PINNED_KEY = 'superdev.sidebar_project_pinned.v1'
const VISIBLE_PROJECT_CHIP_COUNT = 3
const recentProjectIds = ref(readStoredProjectIds(PROJECT_RECENT_KEY))
const pinnedProjectIds = ref(readStoredProjectIds(PROJECT_PINNED_KEY))
const step1ApprovedSample = ref(false)
const legacyDismissPending = ref(
  localStorage.getItem(COMPLETED_STEPS_KEY) === null && localStorage.getItem(DISMISSED_KEY) === null,
)
const selectedProject = computed(() =>
  agentStore.projects.find(project => project.id === selectedProjectId.value)
  ?? agentStore.projects[0]
  ?? null,
)
const orderedProjects = computed(() => {
  const projectsById = new Map(agentStore.projects.map(project => [project.id, project]))
  const seen = new Set<string>()
  const ordered: typeof agentStore.projects = []
  const pushProject = (projectId: string) => {
    if (seen.has(projectId)) return
    const project = projectsById.get(projectId)
    if (!project) return
    seen.add(projectId)
    ordered.push(project)
  }

  for (const projectId of pinnedProjectIds.value) pushProject(projectId)
  for (const projectId of recentProjectIds.value) pushProject(projectId)
  for (const project of agentStore.projects) pushProject(project.id)
  return ordered
})
const visibleProjects = computed(() => {
  const visible = orderedProjects.value.slice(0, VISIBLE_PROJECT_CHIP_COUNT)
  const current = selectedProject.value
  if (!current || visible.some(project => project.id === current.id)) return visible

  // 当前项目必须留在 chips 中，否则从“更多”切换后用户会看不到自己刚选中的上下文。
  if (visible.length >= VISIBLE_PROJECT_CHIP_COUNT) {
    return [...visible.slice(0, VISIBLE_PROJECT_CHIP_COUNT - 1), current]
  }
  return [...visible, current]
})

watch(
  () => agentStore.projects.map(project => project.id),
  (projectIds) => {
    if (projectIds.length === 0) {
      selectedProjectId.value = null
      return
    }
    normalizeProjectPreferences(projectIds)
    if (!selectedProjectId.value || !projectIds.includes(selectedProjectId.value)) {
      selectedProjectId.value = firstAvailableProjectId(projectIds)
    }
  },
  { immediate: true },
)

watch(
  () => agentStore.projects.map(project => `${project.id}:${project.root_path}`).join('|'),
  () => {
    void checkSampleApproval()
  },
  { immediate: true },
)

watch(
  () => [
    agentStore.projects,
    nodeStore.nodesList,
    settingsStore.agentSettings,
    step1ApprovedSample.value,
  ],
  () => runGettingStartedReconcile(),
  { deep: true, immediate: true },
)

function openDeployment(payload: { deploymentId: string; title: string }) {
  workspace.openDeployment(payload.deploymentId, payload.title)
}

function selectProject(projectId: string) {
  selectedProjectId.value = projectId
  serviceQuery.value = ''
  recordProjectOpen(projectId)
}

function toggleProjectPin(projectId: string) {
  const current = pinnedProjectIds.value
  pinnedProjectIds.value = current.includes(projectId)
    ? current.filter(id => id !== projectId)
    : [projectId, ...current]
  persistProjectIds(PROJECT_PINNED_KEY, pinnedProjectIds.value)
}

function openProjectSearch(projectId: string) {
  workspace.openSearch(projectId)
}

function openProjectOverview(projectId: string) {
  workspace.openProjectOverview(projectId)
}

function openNodeCenter() {
  workspace.openNodesTab()
}

async function checkSampleApproval() {
  const sample = agentStore.projects.find(project => isSampleProject(project))
  if (!sample) {
    step1ApprovedSample.value = false
    legacyDismissPending.value = false
    return
  }
  try {
    const audit = await api.listOperationAudit({ project_id: sample.id, limit: 50 })
    step1ApprovedSample.value = (audit.events ?? []).some(event => event.action === 'approved')
  } catch {
    step1ApprovedSample.value = false
  }
  if (legacyDismissPending.value) {
    runGettingStartedReconcile(true)
    legacyDismissPending.value = false
  }
}

function runGettingStartedReconcile(allowLegacyDismiss = false) {
  const detection = deriveDetection({
    onboardingCompleted: settingsStore.agentSettings.onboarding_completed === true,
    sampleSeeded: settingsStore.agentSettings.sample_seeded === true,
    projects: agentStore.projects,
    nodes: nodeStore.nodesList,
    step1ApprovedSample: step1ApprovedSample.value,
  })
  gettingStarted.reconcile(detection)
  // 老用户首次升级时若主线前三步已完成，直接关闭入口，避免空降一个已走过的引导。
  if (allowLegacyDismiss && detection.step0 && detection.step1 && detection.step2) {
    gettingStarted.dismiss()
  }
}

function servicesForEnv(services: Service[], envName: string): Service[] {
  const query = serviceQuery.value.trim().toLowerCase()
  return services
    .filter(svc => svc.deployments?.some(d => d.env_name === envName))
    .filter(svc => !query || svc.name.toLowerCase().includes(query))
}

function readStoredProjectIds(key: string): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(key) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

function persistProjectIds(key: string, ids: string[]) {
  localStorage.setItem(key, JSON.stringify(ids))
}

function normalizeProjectPreferences(projectIds: string[]) {
  const valid = new Set(projectIds)
  const normalizedPinned = pinnedProjectIds.value.filter(id => valid.has(id))
  const normalizedRecent = recentProjectIds.value.filter(id => valid.has(id))
  if (normalizedPinned.length !== pinnedProjectIds.value.length) {
    pinnedProjectIds.value = normalizedPinned
    persistProjectIds(PROJECT_PINNED_KEY, normalizedPinned)
  }
  if (normalizedRecent.length !== recentProjectIds.value.length) {
    recentProjectIds.value = normalizedRecent
    persistProjectIds(PROJECT_RECENT_KEY, normalizedRecent)
  }
}

function firstAvailableProjectId(projectIds: string[]): string {
  const recent = recentProjectIds.value.find(id => projectIds.includes(id))
  return recent ?? projectIds[0]
}

function recordProjectOpen(projectId: string) {
  const valid = new Set(agentStore.projects.map(project => project.id))
  recentProjectIds.value = [
    projectId,
    ...recentProjectIds.value.filter(id => id !== projectId),
  ].filter(id => valid.has(id)).slice(0, 20)
  persistProjectIds(PROJECT_RECENT_KEY, recentProjectIds.value)
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
          :visible-projects="visibleProjects"
          :pinned-project-ids="pinnedProjectIds"
          @select-project="selectProject"
          @toggle-pin="toggleProjectPin"
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
            v-for="env in selectedProject.environments ?? []"
            :key="env.id || env.name"
            :env-name="env.name"
            :is-dev="env.is_dev"
            :initially-expanded="true"
            :project-id="selectedProject.id"
            :services="servicesForEnv(selectedProject.services, env.name)"
            :selected-service-ids="openDeploymentIdSet()"
            @open-deployment="openDeployment"
            @search="openProjectSearch(selectedProject.id)"
          />
        </div>
      </template>
    </div>
    <div class="sidebar-tools">
      <GettingStartedEntry />
      <button
        type="button"
        class="sidebar-tool-button"
        data-test="sidebar-node-center"
        @click="openNodeCenter"
      >
        <span class="sidebar-tool-icon node-center-icon" aria-hidden="true"></span>
        <span class="sidebar-tool-main">{{ t('shell.sidebar.nodeCenter') }}</span>
        <span class="sidebar-tool-hint">{{ t('shell.sidebar.nodeCenterHint') }}</span>
      </button>
      <button
        data-test="sidebar-settings"
        type="button"
        class="sidebar-tool-button"
        @click="router.push('/settings')"
      >
        <span class="sidebar-tool-icon settings-icon" aria-hidden="true">⚙</span>
        <span class="sidebar-tool-main">{{ t('shell.sidebar.settings') }}</span>
        <span class="sidebar-tool-hint">{{ t('shell.sidebar.settingsHint') }}</span>
      </button>
    </div>
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
.sidebar-tools {
  display: grid;
  gap: 3px;
  padding: 8px;
  border-top: 1px solid var(--border-secondary);
}
.sidebar-tool-button {
  display: grid;
  width: 100%;
  min-height: 38px;
  grid-template-columns: 20px minmax(0, auto) minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
  transition: background 0.12s, color 0.12s;
}
.sidebar-tool-button:hover {
  background: rgba(24, 39, 54, 0.58);
  color: var(--text-primary);
}
.sidebar-tool-icon {
  display: inline-flex;
  width: 16px;
  height: 16px;
  align-items: center;
  justify-content: center;
  justify-self: center;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1;
}
.node-center-icon::before {
  width: 8px;
  height: 8px;
  border: 2px solid rgba(63, 185, 80, 0.28);
  border-radius: 50%;
  background: var(--status-running);
  content: '';
}
.settings-icon {
  font-size: 13px;
  opacity: 0.86;
}
.sidebar-tool-main {
  min-width: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
  white-space: nowrap;
}
.sidebar-tool-hint {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
