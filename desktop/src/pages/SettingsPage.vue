<!--
设置页

职责：
  - 展示和修改通用设置
  - 管理项目列表中的本地展示偏好和启动选择

边界：
  - 不处理 MCP 配置
  - 不直接启动或停止服务
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { open, message, ask } from '@tauri-apps/plugin-dialog'
import { api } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { usePipelineTemplateStore } from '@/stores/pipelineTemplate'
import { useSettingsStore } from '@/stores/settings'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import DNSProviderTab from '@/components/Settings/DNSProviderTab.vue'
import OperationApprovalsTab from '@/components/Settings/OperationApprovalsTab.vue'
import TemplateManagerTab from '@/components/Settings/TemplateManagerTab.vue'
import TemplateContentModal from '@/components/Settings/TemplateContentModal.vue'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import type { SupportedLocale } from '@/i18n'
import type { PipelineTemplateDetail, PipelineTemplateSummary, Project, Service } from '@/api/agent'

type SettingsTab = 'general' | 'projects' | 'hosts' | 'dns' | 'templates' | 'approvals'

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
    : route.query.tab === 'dns' || route.query.tab === 'ingress'
      ? 'dns'
    : route.query.tab === 'templates'
      ? 'templates'
      : route.query.tab === 'approvals'
        ? 'approvals'
        : 'general',
)

onMounted(() => {
  void settingsStore.loadAgentSettings()
  void settingsStore.loadAutostart()
  void pipelineTemplateStore.loadTemplates().catch(() => undefined)
  void operationApprovalStore.loadPending()
})

const editorProject = ref<Project | null>(null)
const editorIsNew = ref(false)
const pipelineEditorProject = ref<Project | null>(null)
const templateModalOpen = ref(false)
const selectedTemplate = ref<PipelineTemplateSummary | null>(null)
const templateDetailLoading = ref(false)
const templateDetailError = ref('')
const templateDetail = ref<PipelineTemplateDetail | null>(null)

function openEditor(project: Project) {
  editorProject.value = project
  editorIsNew.value = false
}

function onEditorSaved() {
  editorProject.value = null
  editorIsNew.value = false
}

function openPipelineEditor(project: Project) {
  pipelineEditorProject.value = project
}

function onPipelineEditorSaved() {
  pipelineEditorProject.value = null
}

/**
 * tryImportVscodeLaunch 尝试从项目的 .vscode/launch.json 导入启动配置。
 *
 * 后端 GET /api/projects/{id}/vscode-launch 已完成 launch.json 解析与命令构造
 * （按 type 生成 go run / npm 等命令、替换 ${workspaceFolder}、提取 env）。
 * 本函数仅负责：询问用户 → 把后端返回的配置填入草稿 service（绑定 dev 环境）。
 *
 * 参数：
 *   - created: 刚落地的项目（services 可能为空骨架）
 *
 * 注意：
 *   - 仅当后端返回非空配置、且项目当前无 service 时才导入，避免覆盖已有 config
 *   - 草稿仅在内存中修改，进入编辑器后由用户确认再保存
 */
async function tryImportVscodeLaunch(created: Project): Promise<void> {
  let configs
  try {
    configs = await api.getVscodeLaunch(created.id)
  } catch {
    // 无 launch.json 或解析失败时静默跳过，不阻塞添加项目
    return
  }
  if (!configs || configs.length === 0) return

  const confirmed = await ask(
    t('settings.projects.importVscodeMessage', { count: configs.length }),
    { title: t('settings.projects.importVscodeTitle'), kind: 'info' },
  )
  if (!confirmed) return

  // 已有 service（来自已有 config 文件）时不覆盖
  if (created.services && created.services.length > 0) return

  // 确保 dev 环境存在：无则自动创建并绑定导入的服务
  if (!created.environments) created.environments = []
  let devEnv = created.environments.find(e => e.is_dev) ?? created.environments[0]
  if (!devEnv) {
    devEnv = { id: '', name: 'dev', is_dev: true, order: 0 }
    created.environments.push(devEnv)
  }
  const devEnvName = devEnv.name

  created.services = configs.map((c, i) => ({
    id: '',
    project_id: created.id,
    name: c.name,
    required: false,
    order: i,
    status: '' as const,
    deployments: [{
      id: '',
      env_name: devEnvName,
      location: 'local' as const,
      command: c.command,
      work_dir: c.work_dir,
      env: c.env,
      status: '',
    }],
  }))
}

async function addProject() {
  const selected = await open({ directory: true, multiple: false, title: t('settings.projects.selectProjectRootTitle') })
  if (!selected || Array.isArray(selected)) return
  try {
    // 落地项目（空目录返回空骨架，已有 config 则解析），再进编辑器
    const created = await agentStore.addProject(selected)

    // 尝试导入 .vscode/launch.json（后端解析，本函数仅填充草稿）
    await tryImportVscodeLaunch(created)

    editorProject.value = created
    editorIsNew.value = true
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    await message(msg, { title: t('settings.projects.unableAddProject'), kind: 'error' })
  }
}

async function importPipelineTemplate() {
  const selected = await open({
    multiple: false,
    filters: [{ name: 'YAML', extensions: ['yaml', 'yml'] }],
    title: t('settings.templates.importTitle'),
  })
  if (!selected || Array.isArray(selected)) return
  try {
    await pipelineTemplateStore.importTemplate(selected)
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    await message(msg, { title: t('settings.templates.unableImport'), kind: 'error' })
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

// 设置页项目面板无 env 选择，启动选中统一作用于项目的开发环境。
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
</script>

<template>
  <div class="settings-page">
    <aside class="settings-sidebar">
      <button class="back-btn" @click="router.push('/')">← {{ t('common.back') }}</button>
      <button
        data-test="settings-tab-general"
        class="tab-btn"
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
        class="tab-btn"
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
        class="tab-btn"
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
        data-test="settings-tab-dns"
        class="tab-btn"
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
        data-test="settings-tab-templates"
        class="tab-btn"
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
        class="tab-btn"
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
    </aside>

    <main class="settings-main">
      <section v-if="selectedTab === 'general'" class="pane">
        <header class="pane-header">
          <h1>{{ t('settings.general.title') }}</h1>
        </header>
        <div class="setting-row">
          <div>
            <div class="setting-title">{{ t('settings.general.logRetentionTitle') }}</div>
            <div class="setting-desc">{{ t('settings.general.logRetentionDesc') }}</div>
          </div>
          <input
            data-test="retention-days"
            class="number-input"
            type="number"
            min="1"
            max="90"
            :value="retentionDays"
            @change="retentionDays = Number(($event.target as HTMLInputElement).value)"
          />
        </div>
        <div class="setting-row">
          <div>
            <div class="setting-title">{{ t('settings.general.languageTitle') }}</div>
            <div class="setting-desc">{{ t('settings.general.languageDesc') }}</div>
          </div>
          <select
            data-test="locale-select"
            class="select-input"
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
        <div class="setting-row">
          <div>
            <div class="setting-title">{{ t('settings.general.autostartTitle') }}</div>
            <div class="setting-desc">{{ t('settings.general.autostartDesc') }}</div>
          </div>
          <label class="switch">
            <input
              type="checkbox"
              :checked="settingsStore.autostartEnabled"
              @change="settingsStore.setAutostart(($event.target as HTMLInputElement).checked)"
            />
            <span />
          </label>
        </div>
        <div class="setting-row">
          <div>
            <div class="setting-title">{{ t('settings.general.onboardingTitle') }}</div>
            <div class="setting-desc">{{ t('settings.general.onboardingDesc') }}</div>
          </div>
          <button
            class="secondary-btn"
            data-test="rerun-onboarding"
            type="button"
            @click="router.push('/onboarding')"
          >
            {{ t('settings.general.onboardingAction') }}
          </button>
        </div>
      </section>

      <section v-else-if="selectedTab === 'projects'" class="pane">
        <header class="pane-header">
          <h1>{{ t('settings.projects.title') }}</h1>
          <button class="primary-btn" @click="addProject">+ {{ t('settings.projects.addProject') }}</button>
        </header>
        <div class="project-list">
          <article v-for="project in agentStore.projects" :key="project.id" class="project-card">
            <header class="project-header">
              <div>
                <h2>{{ project.name }}</h2>
                <p>{{ project.root_path }}</p>
              </div>
              <div class="project-actions">
                <span>{{ t('common.serviceCount', { count: project.services.length }) }}</span>
                <button
                  class="ghost-btn"
                  :data-test="`setup-project-${project.id}`"
                  @click="openEditor(project)"
                >
                  {{ t('settings.projects.editConfig') }}
                </button>
                <button
                  class="ghost-btn"
                  :data-test="`pipeline-project-${project.id}`"
                  @click="openPipelineEditor(project)"
                >
                  {{ t('settings.projects.editPipeline') }}
                </button>
                <button class="danger-btn" @click="deleteProject(project)">{{ t('common.delete') }}</button>
              </div>
            </header>
            <div class="service-table">
              <div v-for="service in project.services" :key="service.id" class="service-row">
                <div>
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
                  class="ghost-btn"
                  @click="settingsStore.toggleServiceHidden(service.id)"
                >
                  {{ settingsStore.isServiceHidden(service.id) ? t('common.hidden') : t('common.display') }}
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>

      <section v-else-if="selectedTab === 'hosts'" class="pane">
        <HostManagerTab />
      </section>

      <section v-else-if="selectedTab === 'dns'" class="pane">
        <DNSProviderTab />
      </section>

      <section v-else-if="selectedTab === 'templates'" class="pane">
        <TemplateManagerTab
          :templates="pipelineTemplateStore.templates"
          :on-import="importPipelineTemplate"
          :on-view="viewTemplate"
        />
      </section>

      <section v-else class="pane">
        <OperationApprovalsTab />
      </section>
    </main>

    <ProjectConfigEditor
      v-if="editorProject"
      :project="editorProject"
      :is-new="editorIsNew"
      @saved="onEditorSaved"
      @cancel="editorProject = null; editorIsNew = false"
    />
    <ProjectPipelineEditor
      v-if="pipelineEditorProject"
      :project="pipelineEditorProject"
      :pipeline-templates="pipelineTemplateStore.templates"
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
.settings-page {
  display: flex;
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.settings-sidebar {
  width: 160px;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.back-btn,
.tab-btn {
  display: flex;
  align-items: center;
  text-align: left;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  padding: 8px 10px;
  cursor: pointer;
}
.tab-btn.active {
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
.settings-main {
  flex: 1;
  overflow-y: auto;
}
.pane {
  max-width: 860px;
  padding: 22px;
}
.pane-header,
.project-header,
.setting-row,
.service-row,
.project-actions {
  display: flex;
  align-items: center;
}
.pane-header,
.project-header,
.setting-row {
  justify-content: space-between;
}
h1 {
  margin: 0 0 16px;
  font-size: 18px;
}
h2 {
  margin: 0;
  font-size: 14px;
}
p {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 11px;
}
.setting-row,
.project-card {
  border: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  border-radius: 8px;
}
.setting-row {
  padding: 14px 16px;
  margin-bottom: 10px;
}
.setting-title {
  font-size: 13px;
  font-weight: 600;
}
.setting-desc {
  margin-top: 3px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.number-input {
  width: 72px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-primary);
  padding: 5px 7px;
}
.select-input {
  min-width: 132px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-primary);
  padding: 5px 7px;
  font-size: 12px;
}
.switch input {
  display: none;
}
.switch span {
  width: 34px;
  height: 18px;
  border-radius: 999px;
  background: var(--border);
  display: block;
  position: relative;
}
.switch span::after {
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
.switch input:checked + span {
  background: var(--accent);
}
.switch input:checked + span::after {
  transform: translateX(16px);
  background: #fff;
}
.primary-btn,
.danger-btn,
.ghost-btn {
  border-radius: 5px;
  border: 1px solid var(--border);
  padding: 5px 9px;
  cursor: pointer;
  font-size: 11px;
}
.primary-btn {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.danger-btn {
  background: transparent;
  color: var(--status-failed);
}
.ghost-btn {
  background: var(--bg-overlay);
  color: var(--text-secondary);
}
.project-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.project-card {
  overflow: hidden;
}
.project-header {
  padding: 12px 14px;
  border-bottom: 1px solid var(--border-secondary);
}
.project-actions {
  gap: 10px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.service-table {
  padding: 6px 10px 10px;
}
.service-row {
  justify-content: space-between;
  min-height: 32px;
  border-bottom: 1px solid var(--border-secondary);
}
.service-row:last-child {
  border-bottom: none;
}
.service-name {
  font-size: 12px;
}
.required-badge {
  margin-left: 6px;
  color: var(--accent);
  font-size: 10px;
}
.inline-check {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--text-secondary);
  font-size: 11px;
}
</style>
