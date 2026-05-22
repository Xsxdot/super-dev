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
import { open, message } from '@tauri-apps/plugin-dialog'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
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

async function deleteProject(project: Project) {
  await agentStore.deleteProject(project.id)
}

function selectedStartNames(project: Project): string[] {
  const selected = new Set(project.selected_service_ids ?? [])
  for (const service of project.services) {
    if (service.required) selected.add(service.name)
  }
  return [...selected]
}

async function toggleStartSelection(project: Project, service: Service, checked: boolean) {
  if (service.required) return
  const selected = new Set(selectedStartNames(project))
  if (checked) selected.add(service.name)
  else selected.delete(service.name)
  await agentStore.updateSelected(project.id, [...selected])
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
