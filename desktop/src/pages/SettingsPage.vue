<!--
设置页

职责：
  - 展示和修改通用设置
  - 管理项目列表中的本地展示偏好和启动选择

边界：
  - 不直接读写 MCP 配置文件，MCP 管理由专用 Tauri command 完成
  - 不直接启动或停止服务
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { message, open } from '@tauri-apps/plugin-dialog'
import { useAgentStore } from '@/stores/agent'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { usePipelineTemplateStore } from '@/stores/pipelineTemplate'
import { useSettingsStore } from '@/stores/settings'
import { useAddProjectFlow } from '@/composables/useAddProjectFlow'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import AgentManagerTab from '@/components/Settings/AgentManagerTab.vue'
import DNSProviderTab from '@/components/Settings/DNSProviderTab.vue'
import CertificateTab from '@/components/Settings/CertificateTab.vue'
import McpManagerTab from '@/components/Settings/McpManagerTab.vue'
import OperationApprovalsTab from '@/components/Settings/OperationApprovalsTab.vue'
import TemplateManagerTab from '@/components/Settings/TemplateManagerTab.vue'
import TemplateContentModal from '@/components/Settings/TemplateContentModal.vue'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import type { SupportedLocale } from '@/i18n'
import type { PipelineTemplateDetail, PipelineTemplateSummary, Project, Service } from '@/api/agent'

type SettingsTab = 'general' | 'projects' | 'hosts' | 'agents' | 'dns' | 'ssl' | 'templates' | 'approvals' | 'mcp'

const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const operationApprovalStore = useOperationApprovalStore()
const pipelineTemplateStore = usePipelineTemplateStore()
const settingsStore = useSettingsStore()
const { t } = useI18n()
const selectedTab = ref<SettingsTab>(
  route.query.tab === 'hosts'
    ? 'hosts'
    : route.query.tab === 'agents'
      ? 'agents'
      : route.query.tab === 'dns' || route.query.tab === 'ingress'
        ? 'dns'
        : route.query.tab === 'ssl'
          ? 'ssl'
          : route.query.tab === 'templates'
            ? 'templates'
            : route.query.tab === 'approvals'
              ? 'approvals'
              : route.query.tab === 'mcp'
                ? 'mcp'
                : 'general',
)

onMounted(() => {
  void settingsStore.loadAgentSettings()
  void settingsStore.loadAutostart()
  void pipelineTemplateStore.loadTemplates().catch(() => undefined)
  void operationApprovalStore.loadPending()
})

const pipelineEditorProject = ref<Project | null>(null)
const templateModalOpen = ref(false)
const selectedTemplate = ref<PipelineTemplateSummary | null>(null)
const templateDetailLoading = ref(false)
const templateDetailError = ref('')
const templateDetail = ref<PipelineTemplateDetail | null>(null)
const {
  editorProject,
  editorIsNew,
  addProject,
  openExistingProjectEditor,
  closeEditor,
  onEditorSaved,
} = useAddProjectFlow()

function openPipelineEditor(project: Project) {
  pipelineEditorProject.value = project
}

function onPipelineEditorSaved() {
  pipelineEditorProject.value = null
}

async function importPipelineTemplate() {
  const selected = await open({
    multiple: false,
    filters: [{ name: 'YAML', extensions: ['yaml', 'yml'] }],
    title: t('settings.templates.importTitle'),
  })
  if (!selected || Array.isArray(selected)) return
  try {
    return await pipelineTemplateStore.importTemplate(selected)
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    await message(msg, { title: t('settings.templates.unableImport'), kind: 'error' })
    return undefined
  }
}

async function viewTemplate(template: PipelineTemplateSummary) {
  selectedTemplate.value = template
  templateModalOpen.value = true
  templateDetailError.value = ''
  templateDetail.value = null
  templateDetailLoading.value = true
  try {
    const detail = await pipelineTemplateStore.loadTemplateDetail(template.source, template.id, template.version)
    templateDetail.value = detail
  } catch (error) {
    templateDetailError.value = error instanceof Error ? error.message : String(error)
  } finally {
    templateDetailLoading.value = false
  }
}

async function deleteProject(project: Project) {
  await agentStore.deleteProject(project.id)
}

// 设置页项目面板无 env 选择，快捷启动统一作用于项目的开发环境。
function selectedStartNames(project: Project): string[] {
  const envName = agentStore.devEnvName(project.id)
  const selected = new Set(project.env_selected_service_ids?.[envName] ?? [])
  for (const service of project.services) {
    if (service.required) selected.add(service.name)
  }
  return [...selected]
}

async function toggleStartSelection(project: Project, service: Service, checked: boolean) {
  if (service.required) return
  const envName = agentStore.devEnvName(project.id)
  // 仅在已选集合基础上增删，required 由后端/读取侧补齐，不写入持久化列表。
  const current = new Set(project.env_selected_service_ids?.[envName] ?? [])
  if (checked) current.add(service.name)
  else current.delete(service.name)
  await agentStore.putEnvSelected(project.id, envName, [...current])
}

function isSelectedForStart(project: Project, service: Service): boolean {
  if (service.required) return true
  return selectedStartNames(project).includes(service.name)
}

const retentionDays = computed({
  get: () => settingsStore.agentSettings.log_retention_days,
  set: value => {
    const days = Math.min(90, Math.max(1, Number(value)))
    void settingsStore.saveLogRetentionDays(days)
  },
})

const artifactKeepVersions = computed({
  get: () => settingsStore.agentSettings.artifact_keep_versions,
  set: value => {
    // 与后端 ValidateAgentSettings 的 1~100 范围保持一致，避免提交后被拒。
    const versions = Math.min(100, Math.max(1, Number(value)))
    void settingsStore.saveArtifactKeepVersions(versions)
  },
})
</script>

<template>
  <div class="settings-shell">
    <aside class="settings-sidebar settings-nav" data-test="settings-sidebar">
      <div
        class="settings-titlebar-spacer"
        data-test="settings-titlebar-spacer"
        data-tauri-drag-region
      />
      <button class="settings-nav-back" data-test="settings-back" @click="router.push('/')">← {{ t('common.back') }}</button>
      <button
        data-test="settings-tab-general"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'general' }"
        @click="selectedTab = 'general'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <circle cx="8" cy="8" r="2.5" stroke="currentColor" stroke-width="1.4"/>
          <path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.22 3.22l1.41 1.41M11.37 11.37l1.41 1.41M3.22 12.78l1.41-1.41M11.37 4.63l1.41-1.41" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
        </svg>
        {{ t('settings.tabs.general') }}
      </button>
      <button
        data-test="settings-tab-projects"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'projects' }"
        @click="selectedTab = 'projects'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <rect x="1.5" y="1.5" width="13" height="13" rx="2" stroke="currentColor" stroke-width="1.4"/>
          <path d="M4 5h8M4 8h8M4 11h5" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
        </svg>
        {{ t('settings.tabs.projects') }}
      </button>
      <button
        data-test="settings-tab-hosts"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'hosts' }"
        @click="selectedTab = 'hosts'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <rect x="2" y="3" width="12" height="3" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <rect x="2" y="10" width="12" height="3" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <circle cx="4" cy="4.5" r="0.6" fill="currentColor"/>
          <circle cx="4" cy="11.5" r="0.6" fill="currentColor"/>
        </svg>
        {{ t('settings.tabs.hosts') }}
      </button>
      <button
        data-test="settings-tab-agents"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'agents' }"
        @click="selectedTab = 'agents'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <path d="M3 4h10v8H3z" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <path d="M5 6.5h6M5 9.5h3" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>
          <circle cx="12" cy="11.5" r="1.3" fill="currentColor"/>
        </svg>
        {{ t('settings.tabs.agents') }}
      </button>
      <button
        data-test="settings-tab-dns"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'dns' }"
        @click="selectedTab = 'dns'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.4"/>
          <path d="M2.5 8h11M8 2.5c1.6 1.6 2.3 3.4 2.3 5.5S9.6 11.9 8 13.5C6.4 11.9 5.7 10.1 5.7 8S6.4 4.1 8 2.5z" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        {{ t('settings.tabs.dnsProvider') }}
      </button>
      <button
        data-test="settings-tab-ssl"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'ssl' }"
        @click="selectedTab = 'ssl'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <path d="M8 1.8l4.8 1.8v3.7c0 3-1.8 5.5-4.8 6.9-3-1.4-4.8-3.9-4.8-6.9V3.6z" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <path d="M6 8h4M8 6v4" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
        </svg>
        {{ t('settings.tabs.sslCertificates') }}
      </button>
      <button
        data-test="settings-tab-templates"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'templates' }"
        @click="selectedTab = 'templates'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <path d="M3 2.5h7l3 3v8H3z" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <path d="M10 2.5v3h3M5 8h6M5 10.5h6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
        </svg>
        {{ t('settings.tabs.templates') }}
      </button>
      <button
        data-test="settings-tab-approvals"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'approvals' }"
        @click="selectedTab = 'approvals'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <path d="M8 1.8l5 2v3.7c0 3.1-1.8 5.8-5 7-3.2-1.2-5-3.9-5-7V3.8z" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <path d="M5.5 8.2l1.5 1.5 3.3-3.4" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        {{ t('settings.tabs.approvals') }}
        <span v-if="operationApprovalStore.pendingCount > 0" class="tab-count">{{ operationApprovalStore.pendingCount }}</span>
      </button>
      <button
        data-test="settings-tab-mcp"
        class="settings-nav-item"
        :class="{ active: selectedTab === 'mcp' }"
        @click="selectedTab = 'mcp'"
      >
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style="vertical-align:middle;margin-right:5px">
          <path d="M3 4.5h10v7H3z" stroke="currentColor" stroke-width="1.4" fill="none"/>
          <path d="M5 7h2M9 7h2M5 9.5h6" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>
        </svg>
        {{ t('settings.tabs.mcp') }}
      </button>
    </aside>

    <main class="settings-main">
      <section v-if="selectedTab === 'general'" class="settings-pane">
        <header class="settings-pane-header">
          <div>
            <h1 class="settings-pane-title">{{ t('settings.general.title') }}</h1>
          </div>
        </header>
        <div class="settings-surface">
          <div class="settings-row">
            <div>
              <div class="settings-row-title">{{ t('settings.general.logRetentionTitle') }}</div>
              <div class="settings-row-description">{{ t('settings.general.logRetentionDesc') }}</div>
            </div>
            <input
              data-test="retention-days"
              class="settings-input retention-input"
              type="number"
              min="1"
              max="90"
              :value="retentionDays"
              @change="retentionDays = Number(($event.target as HTMLInputElement).value)"
            />
          </div>
          <div class="settings-row">
            <div>
              <div class="settings-row-title">{{ t('settings.general.artifactKeepTitle') }}</div>
              <div class="settings-row-description">{{ t('settings.general.artifactKeepDesc') }}</div>
            </div>
            <input
              data-test="artifact-keep-versions"
              class="settings-input retention-input"
              type="number"
              min="1"
              max="100"
              :value="artifactKeepVersions"
              @change="artifactKeepVersions = Number(($event.target as HTMLInputElement).value)"
            />
          </div>
          <div class="settings-row">
            <div>
              <div class="settings-row-title">{{ t('settings.general.languageTitle') }}</div>
              <div class="settings-row-description">{{ t('settings.general.languageDesc') }}</div>
            </div>
            <select
              data-test="locale-select"
              class="settings-select locale-select"
              :value="settingsStore.locale"
              @change="settingsStore.setLocale(($event.target as HTMLSelectElement).value as SupportedLocale)"
            >
              <option
                v-for="option in settingsStore.supportedLocaleOptions"
                :key="option.value"
                :value="option.value"
              >
                {{ option.label }}
              </option>
            </select>
          </div>
          <div class="settings-row">
            <div>
              <div class="settings-row-title">{{ t('settings.general.autostartTitle') }}</div>
              <div class="settings-row-description">{{ t('settings.general.autostartDesc') }}</div>
            </div>
            <label class="settings-switch">
              <input
                type="checkbox"
                :checked="settingsStore.autostartEnabled"
                @change="settingsStore.setAutostart(($event.target as HTMLInputElement).checked)"
              />
              <span />
            </label>
          </div>
          <div class="settings-row">
            <div>
              <div class="settings-row-title">{{ t('settings.general.onboardingTitle') }}</div>
              <div class="settings-row-description">{{ t('settings.general.onboardingDesc') }}</div>
            </div>
            <button
              class="settings-btn settings-btn-secondary"
              data-test="rerun-onboarding"
              type="button"
              @click="router.push('/onboarding')"
            >
              {{ t('settings.general.onboardingAction') }}
            </button>
          </div>
        </div>
      </section>

      <section v-else-if="selectedTab === 'projects'" class="settings-pane">
        <header class="settings-pane-header">
          <div>
            <h1 class="settings-pane-title">{{ t('settings.projects.title') }}</h1>
          </div>
          <button class="settings-btn settings-btn-primary" @click="addProject">+ {{ t('settings.projects.addProject') }}</button>
        </header>
        <div class="settings-card-list">
          <article v-for="project in agentStore.projects" :key="project.id" class="settings-card project-card">
            <header class="settings-card-header project-header">
              <div>
                <h2>{{ project.name }}</h2>
                <p>{{ project.root_path }}</p>
              </div>
              <div class="project-actions">
                <span>{{ t('common.serviceCount', { count: project.services.length }) }}</span>
                <button
                  class="settings-btn settings-btn-secondary"
                  :data-test="`setup-project-${project.id}`"
                  @click="openExistingProjectEditor(project)"
                >
                  {{ t('settings.projects.editConfig') }}
                </button>
                <button
                  class="settings-btn settings-btn-secondary"
                  :data-test="`pipeline-project-${project.id}`"
                  @click="openPipelineEditor(project)"
                >
                  {{ t('settings.projects.editPipeline') }}
                </button>
                <button class="settings-btn settings-btn-danger" @click="deleteProject(project)">{{ t('common.delete') }}</button>
              </div>
            </header>
            <div class="service-table settings-surface">
              <div v-for="service in project.services" :key="service.id" class="service-row">
                <div class="service-main">
                  <span class="service-name">{{ service.name }}</span>
                  <span v-if="service.required" class="required-badge">{{ t('common.required') }}</span>
                </div>
                <label class="inline-check">
                  <input
                    :data-test="`select-start-${service.id}`"
                    type="checkbox"
                    :disabled="service.required"
                    :checked="isSelectedForStart(project, service)"
                    @change="toggleStartSelection(project, service, ($event.target as HTMLInputElement).checked)"
                  />
                  {{ t('settings.projects.startSelected') }}
                </label>
                <button
                  :data-test="`toggle-hidden-${service.id}`"
                  class="settings-btn settings-btn-secondary"
                  @click="settingsStore.toggleServiceHidden(service.id)"
                >
                  {{ settingsStore.isServiceHidden(service.id) ? t('common.hidden') : t('common.display') }}
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>

      <section v-else-if="selectedTab === 'hosts'" class="settings-pane">
        <HostManagerTab />
      </section>

      <section v-else-if="selectedTab === 'agents'" class="settings-pane">
        <AgentManagerTab />
      </section>

      <section v-else-if="selectedTab === 'dns'" class="settings-pane">
        <DNSProviderTab />
      </section>

      <section v-else-if="selectedTab === 'ssl'" class="settings-pane">
        <CertificateTab />
      </section>

      <section v-else-if="selectedTab === 'templates'" class="settings-pane">
        <TemplateManagerTab
          :templates="pipelineTemplateStore.templates"
          :on-import="importPipelineTemplate"
          :on-view="viewTemplate"
        />
      </section>

      <section v-else-if="selectedTab === 'approvals'" class="settings-pane">
        <OperationApprovalsTab />
      </section>

      <section v-else class="settings-pane">
        <McpManagerTab />
      </section>
    </main>

    <ProjectConfigEditor
      v-if="editorProject"
      :project="editorProject"
      :is-new="editorIsNew"
      @saved="onEditorSaved"
      @cancel="closeEditor"
    />
    <ProjectPipelineEditor
      v-if="pipelineEditorProject"
      :project="pipelineEditorProject"
      :pipeline-templates="pipelineTemplateStore.templates"
      :on-import-template="importPipelineTemplate"
      @saved="onPipelineEditorSaved"
      @cancel="pipelineEditorProject = null"
    />
    <TemplateContentModal
      :open="templateModalOpen"
      :title="selectedTemplate?.name ?? t('settings.templates.contentTitle')"
      :yaml="templateDetail?.yaml ?? ''"
      :detail="templateDetail"
      :loading="templateDetailLoading"
      :error="templateDetailError"
      @close="templateModalOpen = false"
    />
  </div>
</template>

<style scoped>
.settings-sidebar {
  padding-top: 0;
}

.settings-titlebar-spacer {
  height: 58px;
  flex-shrink: 0;
  margin: 0 -10px;
}

.settings-nav-back,
.settings-nav-item {
  display: flex;
  align-items: center;
  width: 100%;
  text-align: left;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  padding: 8px 10px;
  cursor: pointer;
  font-size: 12px;
}

.settings-nav-back:hover,
.settings-nav-item:hover {
  background: var(--control-hover);
  color: var(--text-primary);
}

.settings-nav-item.active {
  background: var(--bg-overlay);
  color: var(--text-primary);
}

.tab-count {
  margin-left: auto;
  min-width: 18px;
  height: 18px;
  border-radius: 999px;
  background: var(--accent);
  color: #fff;
  font-size: 11px;
  line-height: 18px;
  text-align: center;
}

.retention-input {
  width: 72px;
}

.locale-select {
  min-width: 156px;
}

.settings-switch input {
  display: none;
}

.settings-switch span {
  width: 34px;
  height: 18px;
  border-radius: 999px;
  background: var(--border);
  display: block;
  position: relative;
}

.settings-switch span::after {
  content: '';
  position: absolute;
  width: 14px;
  height: 14px;
  left: 2px;
  top: 2px;
  border-radius: 50%;
  background: var(--text-secondary);
  transition: transform 0.12s;
}

.settings-switch input:checked + span {
  background: var(--accent);
}

.settings-switch input:checked + span::after {
  transform: translateX(16px);
  background: #fff;
}

.project-card h2 {
  margin: 0;
  font-size: 14px;
}

.project-card p {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 11px;
}

.project-header {
  align-items: center;
}

.project-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--text-tertiary);
  font-size: 11px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.service-table {
  border: 0;
  border-radius: 0;
  background: transparent;
  padding: 6px 10px 10px;
}

.service-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 96px max-content;
  align-items: center;
  gap: 10px;
  min-height: 34px;
  border-bottom: 1px solid var(--border-secondary);
}

.service-row:last-child {
  border-bottom: none;
}

.service-main {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.service-name {
  flex: 0 1 auto;
  min-width: 0;
  max-width: 100%;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
  white-space: nowrap;
}

.required-badge {
  flex: 0 0 auto;
  color: var(--accent);
  font-size: 10px;
}

.inline-check {
  display: flex;
  align-items: center;
  gap: 5px;
  justify-self: start;
  color: var(--text-secondary);
  font-size: 11px;
  white-space: nowrap;
}
</style>
