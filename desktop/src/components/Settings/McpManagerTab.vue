<!--
MCP 管理设置页签

职责：
  - 展示 SuperDev MCP 在各编程智能体中的安装状态
  - 触发 MCP/skill 安装更新和卸载
  - 展示 MCP 工具能力说明与 bundled superdev skill 文档

边界：
  - 不直接读写 Agent 配置文件，统一通过 api/mcpInstall.ts 调用 Tauri command
  - 不修改 onboarding store
  - 不启动、停止或探测 SuperDev agent 运行态
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ask } from '@tauri-apps/plugin-dialog'
import { useI18n } from 'vue-i18n'
import {
  getMcpDocs,
  getMcpInstallHint,
  getMcpStatus,
  installMcp,
  uninstallMcp,
  type CodingAgent,
  type InstallHint,
  type McpDocs,
  type McpDocument,
  type McpStatus,
} from '@/api/mcpInstall'

const agents: Array<{ id: CodingAgent, label: string }> = [
  { id: 'claude-code', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
  { id: 'cursor', label: 'Cursor' },
]

const { t } = useI18n()
const statuses = ref<McpStatus[]>([])
const docs = ref<McpDocs | null>(null)
const loading = ref(false)
const docsLoading = ref(false)
const error = ref('')
const docsError = ref('')
const selectedDocId = ref('overview')
const manualHint = ref<InstallHint | null>(null)
const manualAgentLabel = ref('')
const operationAgent = ref<CodingAgent | null>(null)
const operationMessage = ref<Record<CodingAgent, string>>({
  'claude-code': '',
  codex: '',
  cursor: '',
})

const selectedDocument = computed<McpDocument | null>(() =>
  docs.value?.documents.find(doc => doc.id === selectedDocId.value) ?? null,
)

const agentRows = computed(() =>
  agents.map(agent => ({
    ...agent,
    status: statuses.value.find(status => status.agent === agent.id) ?? null,
  })),
)

onMounted(() => {
  void refreshAll()
})

function errorMessage(errorValue: unknown): string {
  return errorValue instanceof Error ? errorValue.message : String(errorValue)
}

function skillLabel(status: McpStatus | null): string {
  if (!status?.skill_installed) return t('settings.mcp.skillMissing')
  if (status.skill_matches_bundled === true) return t('settings.mcp.skillCurrent')
  return t('settings.mcp.skillOutdated')
}

async function refreshAll() {
  await Promise.all([refreshStatus(), refreshDocs()])
}

async function refreshStatus() {
  loading.value = true
  error.value = ''
  try {
    statuses.value = await getMcpStatus()
  } catch (err) {
    error.value = t('settings.mcp.readFailed', { message: errorMessage(err) })
  } finally {
    loading.value = false
  }
}

async function refreshDocs() {
  docsLoading.value = true
  docsError.value = ''
  try {
    docs.value = await getMcpDocs()
  } catch (err) {
    docsError.value = t('settings.mcp.readFailed', { message: errorMessage(err) })
  } finally {
    docsLoading.value = false
  }
}

async function installOrUpdate(agent: CodingAgent) {
  operationAgent.value = agent
  operationMessage.value[agent] = ''
  try {
    const outcome = await installMcp(agent)
    operationMessage.value[agent] = outcome.already_present && outcome.skill.already_present
      ? t('settings.mcp.installCurrent')
      : t('settings.mcp.installUpdated')
    await refreshStatus()
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
  } finally {
    operationAgent.value = null
  }
}

async function confirmUninstall(agent: CodingAgent, label: string) {
  const confirmed = await ask(t('settings.mcp.uninstallConfirmMessage', { agent: label }), {
    title: t('settings.mcp.uninstallConfirmTitle'),
    kind: 'warning',
  })
  if (!confirmed) return
  operationAgent.value = agent
  operationMessage.value[agent] = ''
  try {
    const outcome = await uninstallMcp(agent)
    const backup = outcome.config_backup_path
      ? ` · ${t('settings.mcp.backupSaved', { path: outcome.config_backup_path })}`
      : ''
    operationMessage.value[agent] = `${t('settings.mcp.uninstallDone')}${backup}`
    await refreshStatus()
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
  } finally {
    operationAgent.value = null
  }
}

async function showManualConfig(agent: CodingAgent, label: string) {
  operationAgent.value = agent
  operationMessage.value[agent] = ''
  try {
    manualHint.value = await getMcpInstallHint(agent)
    manualAgentLabel.value = label
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
  } finally {
    operationAgent.value = null
  }
}
</script>

<template>
  <div>
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.mcp.title') }}</h1>
        <p class="settings-pane-description">{{ t('settings.mcp.description') }}</p>
      </div>
      <button class="settings-btn settings-btn-secondary" data-test="mcp-refresh" type="button" @click="refreshAll">
        {{ t('settings.mcp.refresh') }}
      </button>
    </header>

    <div v-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>
    <div v-if="loading" class="settings-empty">{{ t('settings.mcp.loading') }}</div>

    <div class="settings-card-list mcp-agent-list">
      <article v-for="row in agentRows" :key="row.id" class="settings-card">
        <header class="settings-card-header mcp-card-header">
          <div>
            <h2 class="mcp-agent-name">{{ row.label }}</h2>
            <p class="mcp-agent-path">
              {{ t('settings.mcp.agentStatus') }}:
              {{ row.status?.agent_installed ? t('settings.mcp.detected') : t('settings.mcp.notDetected') }}
              <span v-if="row.status?.detection_path"> · {{ row.status.detection_path }}</span>
            </p>
          </div>
          <div class="settings-toolbar">
            <button
              class="settings-btn settings-btn-primary"
              :data-test="`mcp-install-${row.id}`"
              type="button"
              :disabled="!row.status?.agent_installed || operationAgent === row.id"
              @click="installOrUpdate(row.id)"
            >
              {{ t('settings.mcp.installUpdate') }}
            </button>
            <button
              class="settings-btn settings-btn-danger"
              :data-test="`mcp-uninstall-${row.id}`"
              type="button"
              :disabled="operationAgent === row.id"
              @click="confirmUninstall(row.id, row.label)"
            >
              {{ t('settings.mcp.uninstall') }}
            </button>
            <button
              class="settings-btn settings-btn-secondary"
              :data-test="`mcp-manual-${row.id}`"
              type="button"
              :disabled="operationAgent === row.id"
              @click="showManualConfig(row.id, row.label)"
            >
              {{ t('settings.mcp.manualConfig') }}
            </button>
          </div>
        </header>
        <div class="mcp-detail-grid">
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.configFile') }}</span>
            <code>{{ row.status?.config_path }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>MCP</span>
            <strong>{{ row.status?.mcp_configured ? t('settings.mcp.configured') : t('settings.mcp.notConfigured') }}</strong>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.command') }}</span>
            <code>{{ row.status?.mcp_command ?? t('settings.mcp.noCommand') }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.agentUrl') }}</span>
            <code>{{ row.status?.agent_url ?? t('settings.mcp.noAgentUrl') }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.skill') }}</span>
            <strong>{{ skillLabel(row.status) }}</strong>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.skillPath') }}</span>
            <code>{{ row.status?.skill_path }}</code>
          </div>
        </div>
        <div v-if="row.status?.config_error" class="settings-alert settings-alert-danger mcp-inline-alert">
          {{ row.status.config_error }}
        </div>
        <div v-if="row.status?.skill_error" class="settings-alert settings-alert-warning mcp-inline-alert">
          {{ row.status.skill_error }}
        </div>
        <div v-if="operationMessage[row.id]" class="settings-alert mcp-inline-alert">
          {{ operationMessage[row.id] }}
        </div>
      </article>
    </div>

    <section class="settings-section mcp-docs" data-test="mcp-docs">
      <header class="mcp-section-heading">
        <div>
          <h2>{{ t('settings.mcp.capabilitiesTitle') }}</h2>
          <p>{{ t('settings.mcp.capabilitiesDescription') }}</p>
        </div>
      </header>
      <div v-if="docsError" class="settings-alert settings-alert-danger">{{ docsError }}</div>
      <div v-else-if="docsLoading" class="settings-empty">{{ t('settings.mcp.docsLoading') }}</div>
      <div v-else class="mcp-doc-layout">
        <nav class="mcp-doc-nav">
          <button
            type="button"
            class="settings-btn"
            :class="{ 'settings-btn-primary': selectedDocId === 'overview' }"
            data-test="mcp-doc-overview"
            @click="selectedDocId = 'overview'"
          >
            {{ t('settings.mcp.overview') }}
          </button>
          <button
            v-for="doc in docs?.documents ?? []"
            :key="doc.id"
            type="button"
            class="settings-btn"
            :class="{ 'settings-btn-primary': selectedDocId === doc.id }"
            :data-test="`mcp-doc-${doc.id}`"
            @click="selectedDocId = doc.id"
          >
            {{ doc.title }}
          </button>
        </nav>
        <div class="mcp-doc-body">
          <div v-if="selectedDocId === 'overview'">
            <div class="settings-alert settings-alert-warning">
              <strong>{{ t('settings.mcp.safetyTitle') }}</strong>
              <span>{{ t('settings.mcp.safetyDescription') }}</span>
            </div>
            <section v-for="section in docs?.summary_sections ?? []" :key="section.id" class="mcp-capability-section">
              <h3>{{ section.title }}</h3>
              <p>{{ section.description }}</p>
              <table class="settings-table">
                <thead>
                  <tr>
                    <th>Tool</th>
                    <th>Purpose</th>
                    <th>Access</th>
                    <th>Reference</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="tool in section.tools" :key="tool.name">
                    <td><code>{{ tool.name }}</code></td>
                    <td>{{ tool.purpose }}</td>
                    <td>{{ tool.access }}</td>
                    <td>{{ tool.reference }}</td>
                  </tr>
                </tbody>
              </table>
            </section>
          </div>
          <pre v-else data-test="mcp-doc-content" class="settings-mono mcp-doc-content">{{ selectedDocument?.content }}</pre>
        </div>
      </div>
    </section>

    <div v-if="manualHint" class="settings-modal-backdrop" @click.self="manualHint = null">
      <div class="settings-modal settings-modal-wide">
        <header class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.mcp.manualConfigTitle', { agent: manualAgentLabel }) }}</h2>
          <button class="settings-btn settings-btn-text" type="button" @click="manualHint = null">
            {{ t('common.close') }}
          </button>
        </header>
        <div class="settings-modal-body">
          <p class="mcp-agent-path">{{ t('settings.mcp.configFile') }}: {{ manualHint.config_path }}</p>
          <pre class="settings-mono mcp-doc-content">{{ manualHint.manual_config }}</pre>
          <p class="mcp-agent-path">{{ t('settings.mcp.skillPath') }}: {{ manualHint.skill_target_path }}</p>
        </div>
        <footer class="settings-modal-footer">
          <button class="settings-btn settings-btn-primary" type="button" @click="manualHint = null">
            {{ t('settings.mcp.closeManual') }}
          </button>
        </footer>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mcp-agent-list {
  margin-bottom: 14px;
}

.mcp-card-header {
  align-items: center;
}

.mcp-agent-name {
  margin: 0;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 650;
}

.mcp-agent-path {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1.45;
  word-break: break-all;
}

.mcp-detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  padding: 12px 14px;
}

.mcp-detail-item {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  color: var(--text-tertiary);
  font-size: 11px;
}

.mcp-detail-item strong,
.mcp-detail-item code {
  color: var(--text-primary);
  font-size: 12px;
  word-break: break-all;
}

.mcp-inline-alert {
  margin: 0 14px 12px;
}

.mcp-section-heading {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.mcp-section-heading h2,
.mcp-capability-section h3 {
  margin: 0;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 650;
}

.mcp-section-heading p,
.mcp-capability-section p {
  margin: 4px 0 10px;
  color: var(--text-tertiary);
  font-size: 12px;
  line-height: 1.45;
}

.mcp-doc-layout {
  display: grid;
  grid-template-columns: 190px minmax(0, 1fr);
  gap: 12px;
}

.mcp-doc-nav {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.mcp-doc-body {
  min-width: 0;
}

.mcp-capability-section {
  margin-top: 14px;
}

.mcp-doc-content {
  min-height: 280px;
  max-height: 520px;
  overflow: auto;
  margin: 0;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  padding: 12px;
  white-space: pre-wrap;
  user-select: text;
  -webkit-user-select: text;
}

@media (max-width: 760px) {
  .mcp-detail-grid,
  .mcp-doc-layout {
    grid-template-columns: 1fr;
  }

  .mcp-card-header {
    align-items: flex-start;
  }
}
</style>
