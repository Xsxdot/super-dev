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
import { open, message, ask } from '@tauri-apps/plugin-dialog'
import { api } from '@/api/agent'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import type { Project, Service } from '@/api/agent'

type SettingsTab = 'general' | 'projects' | 'hosts'

const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const settingsStore = useSettingsStore()
const selectedTab = ref<SettingsTab>(
  route.query.tab === 'hosts' ? 'hosts' : 'general',
)

onMounted(() => {
  void settingsStore.loadAgentSettings()
  void settingsStore.loadAutostart()
})

const editorProject = ref<Project | null>(null)
const editorIsNew = ref(false)

function openEditor(project: Project) {
  editorProject.value = project
  editorIsNew.value = false
}

function onEditorSaved() {
  editorProject.value = null
  editorIsNew.value = false
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
    `检测到 .vscode/launch.json，包含 ${configs.length} 个启动配置，是否导入？\n导入后可在编辑器中调整。`,
    { title: '导入 VS Code 启动配置', kind: 'info' },
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
  const selected = await open({ directory: true, multiple: false, title: '选择项目根目录' })
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
    await message(msg, { title: '无法添加项目', kind: 'error' })
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
      <button class="back-btn" @click="router.push('/')">← 返回</button>
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
        通用
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
        项目
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
        主机管理
      </button>
    </aside>

    <main class="settings-main">
      <section v-if="selectedTab === 'general'" class="pane">
        <header class="pane-header">
          <h1>通用</h1>
        </header>
        <div class="setting-row">
          <div>
            <div class="setting-title">日志保留天数</div>
            <div class="setting-desc">超过此天数的日志会在 agent 启动时自动删除</div>
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
            <div class="setting-title">开机自启</div>
            <div class="setting-desc">登录系统后自动启动 SuperDev 桌面应用</div>
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
      </section>

      <section v-else-if="selectedTab === 'projects'" class="pane">
        <header class="pane-header">
          <h1>项目</h1>
          <button class="primary-btn" @click="addProject">+ 添加项目</button>
        </header>
        <div class="project-list">
          <article v-for="project in agentStore.projects" :key="project.id" class="project-card">
            <header class="project-header">
              <div>
                <h2>{{ project.name }}</h2>
                <p>{{ project.root_path }}</p>
              </div>
              <div class="project-actions">
                <span>{{ project.services.length }} 个服务</span>
                <button
                  class="ghost-btn"
                  :data-test="`setup-project-${project.id}`"
                  @click="openEditor(project)"
                >
                  编辑配置
                </button>
                <button class="danger-btn" @click="deleteProject(project)">删除</button>
              </div>
            </header>
            <div class="service-table">
              <div v-for="service in project.services" :key="service.id" class="service-row">
                <div>
                  <span class="service-name">{{ service.name }}</span>
                  <span v-if="service.required" class="required-badge">必选</span>
                </div>
                <label class="inline-check">
                  <input
                    :data-test="`select-start-${service.id}`"
                    type="checkbox"
                    :disabled="service.required"
                    :checked="isSelectedForStart(project, service)"
                    @change="toggleStartSelection(project, service, ($event.target as HTMLInputElement).checked)"
                  />
                  启动选中
                </label>
                <button
                  :data-test="`toggle-hidden-${service.id}`"
                  class="ghost-btn"
                  @click="settingsStore.toggleServiceHidden(service.id)"
                >
                  {{ settingsStore.isServiceHidden(service.id) ? '已隐藏' : '显示' }}
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>

      <section v-else class="pane">
        <HostManagerTab />
      </section>
    </main>

    <ProjectConfigEditor
      v-if="editorProject"
      :project="editorProject"
      :is-new="editorIsNew"
      @saved="onEditorSaved"
      @cancel="editorProject = null; editorIsNew = false"
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
