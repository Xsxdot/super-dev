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
import ManualAgentConnectDialog from '@/components/Onboarding/ManualAgentConnectDialog.vue'
import { emitConnectorDiagnostic } from '@/lib/connectorDiagnostics'
import {
  getMcpDocs,
  getAgentConnectorManualInstructions,
  listAgentConnectors,
  installAgentConnector,
  updateAgentConnector,
  verifyAgentConnector,
  uninstallAgentConnector,
  type ConnectorId,
  type ConnectorManualInstructions,
  type ConnectorOperationOutcome,
  type ConnectorResult,
  type AgentConnectorSummary,
  type McpDocs,
  type McpDocument,
} from '@/api/mcpInstall'

const { t } = useI18n()
const statuses = ref<AgentConnectorSummary[]>([])
const docs = ref<McpDocs | null>(null)
const loading = ref(false)
const docsLoading = ref(false)
const error = ref('')
const docsError = ref('')
const selectedDocId = ref('overview')
const manualHint = ref<ConnectorManualInstructions | null>(null)
const manualAgentLabel = ref('')
const operationAgent = ref<ConnectorId | null>(null)
const operationMessage = ref<Record<string, string>>({})
const operationTone = ref<Record<string, 'success' | 'warning' | 'danger' | 'info'>>({})
/** 写操作 outcome 带来的一次性重启提示；不绑定长期 status.configured。 */
const restartHints = ref<Record<string, boolean>>({})
/** 最近一次 install/update outcome，供 Registry 增量重试。 */
const lastOutcomes = ref<Record<string, ConnectorOperationOutcome>>({})
const showOtherBuiltIns = ref(false)
const manualDialogOpen = ref(false)

const selectedDocument = computed<McpDocument | null>(() =>
  docs.value?.documents.find(doc => doc.id === selectedDocId.value) ?? null,
)

const agentRows = computed(() =>
  statuses.value.map(summary => ({
    id: summary.descriptor.id,
    label: summary.descriptor.display_name,
    status: summary,
  })),
)
const detectedRows = computed(() => agentRows.value.filter(row => row.status.state.detected))
const otherBuiltInRows = computed(() => agentRows.value.filter(row =>
  !row.status.state.detected && row.status.descriptor.built_in,
))
const visibleRows = computed(() => [
  ...detectedRows.value,
  ...(showOtherBuiltIns.value ? otherBuiltInRows.value : []),
])

onMounted(() => {
  void refreshAll()
})

function errorMessage(errorValue: unknown): string {
  return errorValue instanceof Error ? errorValue.message : String(errorValue)
}

function skillLabel(status: AgentConnectorSummary | null): string {
  return status?.state.integrations.find(i => i.capability === 'skill')?.status ?? 'unknown'
}

// hookLabel 描述 SessionStart hook 状态：未装 / 已装但 Codex 需手动信任 / 已装生效。
function hookLabel(status: AgentConnectorSummary | null): string {
  return status?.state.integrations.find(i => i.capability === 'session_hook')?.status ?? 'unknown'
}

function operationSupport(status: AgentConnectorSummary, operation: 'install' | 'update' | 'verify' | 'uninstall') {
  return status.descriptor.operations.find(item => item.operation === operation)?.support ?? 'unsupported'
}

function mcpConfigured(status: AgentConnectorSummary): boolean {
  return status.state.integrations.some(item =>
    item.capability === 'mcp' && item.status === 'configured',
  )
}

/**
 * formatOutcomeMessage 按 ConnectorResult 真值表生成设置页反馈，并附上能力明细。
 *
 * 注意：partial/failed/needs_action 不得伪装成「已更新」。
 */
function formatOutcomeMessage(outcome: ConnectorOperationOutcome): string {
  const baseKey: Record<ConnectorResult, string> = {
    success: 'settings.mcp.installUpdated',
    unchanged: 'settings.mcp.installCurrent',
    partial: 'settings.mcp.installPartial',
    failed: 'settings.mcp.installFailed',
    needs_action: 'settings.mcp.installNeedsAction',
  }
  const base = t(baseKey[outcome.result] ?? 'settings.mcp.actionFailed', {
    message: outcome.message ?? outcome.result,
  })
  const details = outcome.integrations
    .map((item) => {
      const suffix = item.message ? ` (${item.message})` : ''
      return `${item.capability}: ${item.result}${suffix}`
    })
    .join('；')
  const withDetails = details ? `${base} — ${details}` : base
  if (outcome.result === 'failed' && outcome.manual_instructions?.summary) {
    return `${withDetails}；${outcome.manual_instructions.summary}`
  }
  return withDetails
}

function outcomeTone(result: ConnectorResult): 'success' | 'warning' | 'danger' | 'info' {
  if (result === 'failed') return 'danger'
  if (result === 'partial' || result === 'needs_action') return 'warning'
  if (result === 'unchanged') return 'info'
  return 'success'
}

function rememberOutcome(agent: ConnectorId, outcome: ConnectorOperationOutcome) {
  lastOutcomes.value[agent] = outcome
  operationMessage.value[agent] = formatOutcomeMessage(outcome)
  operationTone.value[agent] = outcomeTone(outcome.result)
  restartHints.value[agent] = outcome.requires_restart
}

async function refreshAll() {
  await Promise.all([refreshStatus(), refreshDocs()])
}

async function refreshStatus() {
  const started = performance.now()
  loading.value = true
  error.value = ''
  emitConnectorDiagnostic('list.started', 'info', { surface: 'settings' })
  try {
    statuses.value = await listAgentConnectors()
    emitConnectorDiagnostic('list.succeeded', 'info', {
      surface: 'settings', connectorCount: statuses.value.length,
      durationMs: Math.round(performance.now() - started),
    })
  } catch (err) {
    error.value = t('settings.mcp.readFailed', { message: errorMessage(err) })
    emitConnectorDiagnostic('list.failed', 'error', {
      surface: 'settings', errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
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

async function installOrUpdate(agent: ConnectorId) {
  const started = performance.now()
  operationAgent.value = agent
  operationMessage.value[agent] = ''
  operationTone.value[agent] = 'info'
  try {
    const status = statuses.value.find(item => item.descriptor.id === agent)
    if (!status) throw new Error('Connector status unavailable')
    const updating = mcpConfigured(status)
    const operation = updating ? 'update' : 'install'
    // 传入上次 outcome，让 Registry 只重试 failed/needs_action 及依赖 MCP 未执行的能力。
    const previous = lastOutcomes.value[agent] ?? null
    emitConnectorDiagnostic(`${operation}.started`, 'info', {
      surface: 'settings',
      connectorId: agent,
      hasPreviousOutcome: previous !== null,
    })
    const outcome = updating
      ? await updateAgentConnector(agent, previous)
      : await installAgentConnector(agent, previous)
    rememberOutcome(agent, outcome)
    emitConnectorDiagnostic(`${operation}.completed`, outcome.result === 'failed' ? 'error' : outcome.result === 'partial' ? 'warn' : 'info', {
      surface: 'settings', connectorId: agent, result: outcome.result,
      capabilityResults: outcome.integrations.map(item => `${item.capability}=${item.result}`),
      durationMs: Math.round(performance.now() - started),
    })
    await refreshStatus()
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
    operationTone.value[agent] = 'danger'
    emitConnectorDiagnostic('mutation.failed', 'error', {
      surface: 'settings', connectorId: agent,
      errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
  } finally {
    operationAgent.value = null
  }
}

async function verifyConnector(agent: ConnectorId) {
  const started = performance.now()
  operationAgent.value = agent
  operationMessage.value[agent] = ''
  operationTone.value[agent] = 'info'
  try {
    emitConnectorDiagnostic('verify.started', 'info', { surface: 'settings', connectorId: agent })
    const outcome = await verifyAgentConnector(agent)
    operationMessage.value[agent] = formatOutcomeMessage(outcome)
    operationTone.value[agent] = outcomeTone(outcome.result)
    emitConnectorDiagnostic('verify.completed', outcome.result === 'failed' ? 'error' : 'info', {
      surface: 'settings', connectorId: agent, result: outcome.result,
      capabilityResults: outcome.integrations.map(item => `${item.capability}=${item.result}`),
      durationMs: Math.round(performance.now() - started),
    })
    await refreshStatus()
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
    operationTone.value[agent] = 'danger'
    emitConnectorDiagnostic('verify.failed', 'error', {
      surface: 'settings', connectorId: agent,
      errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
  } finally {
    operationAgent.value = null
  }
}

async function confirmUninstall(agent: ConnectorId, label: string) {
  const confirmed = await ask(t('settings.mcp.uninstallConfirmMessage', { agent: label }), {
    title: t('settings.mcp.uninstallConfirmTitle'),
    kind: 'warning',
  })
  if (!confirmed) return
  operationAgent.value = agent
  const started = performance.now()
  operationMessage.value[agent] = ''
  operationTone.value[agent] = 'info'
  try {
    emitConnectorDiagnostic('uninstall.started', 'info', { surface: 'settings', connectorId: agent })
    const outcome = await uninstallAgentConnector(agent)
    // 卸载后清空 prior outcome，避免下次安装误用旧的 partial 重试集合。
    delete lastOutcomes.value[agent]
    delete restartHints.value[agent]
    operationMessage.value[agent] = outcome.result === 'failed'
      ? formatOutcomeMessage(outcome)
      : t('settings.mcp.uninstallDone')
    operationTone.value[agent] = outcome.result === 'failed' ? 'danger' : 'success'
    emitConnectorDiagnostic('uninstall.completed', outcome.result === 'failed' ? 'error' : 'info', {
      surface: 'settings', connectorId: agent, result: outcome.result,
      capabilityResults: outcome.integrations.map(item => `${item.capability}=${item.result}`),
      durationMs: Math.round(performance.now() - started),
    })
    await refreshStatus()
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
    operationTone.value[agent] = 'danger'
    emitConnectorDiagnostic('uninstall.failed', 'error', {
      surface: 'settings', connectorId: agent,
      errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
  } finally {
    operationAgent.value = null
  }
}

async function showManualConfig(agent: ConnectorId, label: string) {
  const started = performance.now()
  operationAgent.value = agent
  operationMessage.value[agent] = ''
  try {
    emitConnectorDiagnostic('manual.started', 'info', { surface: 'settings', connectorId: agent })
    manualHint.value = await getAgentConnectorManualInstructions(agent)
    manualAgentLabel.value = label
    emitConnectorDiagnostic('manual.completed', 'info', {
      surface: 'settings', connectorId: agent,
      durationMs: Math.round(performance.now() - started),
    })
  } catch (err) {
    operationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
    emitConnectorDiagnostic('manual.failed', 'error', {
      surface: 'settings', connectorId: agent,
      errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
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
      <article v-for="row in visibleRows" :key="row.id" class="settings-card">
        <header class="settings-card-header mcp-card-header">
          <div>
            <h2 class="mcp-agent-name">{{ row.label }}</h2>
            <p class="mcp-agent-path">
              {{ t('settings.mcp.agentStatus') }}:
              {{ row.status?.state.detected ? t('settings.mcp.detected') : t('settings.mcp.notDetected') }}
              <span v-if="row.status?.state.detection_path"> · {{ row.status.state.detection_path }}</span>
            </p>
          </div>
          <div class="settings-toolbar">
            <button
              class="settings-btn settings-btn-primary"
              :data-test="`mcp-install-${row.id}`"
              type="button"
              :disabled="!row.status.state.detected || operationAgent === row.id || operationSupport(row.status, mcpConfigured(row.status) ? 'update' : 'install') !== 'automatic'"
              @click="installOrUpdate(row.id)"
            >
              {{ t('settings.mcp.installUpdate') }}
            </button>
            <button
              class="settings-btn settings-btn-secondary"
              :data-test="`mcp-verify-${row.id}`"
              type="button"
              :disabled="operationAgent === row.id || operationSupport(row.status, 'verify') !== 'automatic'"
              @click="verifyConnector(row.id)"
            >
              {{ t('settings.mcp.verify') }}
            </button>
            <button
              class="settings-btn settings-btn-danger"
              :data-test="`mcp-uninstall-${row.id}`"
              type="button"
              :disabled="operationAgent === row.id || operationSupport(row.status, 'uninstall') !== 'automatic'"
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
        <div class="agent-capabilities">
          <span :data-test="`mcp-support-level-${row.id}`">{{ row.status.descriptor.support_level ?? 'unsupported' }}</span>
          <span v-for="integration in row.status.descriptor.integrations" :key="integration.capability">
            {{ integration.capability }} · {{ integration.support }}
          </span>
        </div>
        <div class="mcp-detail-grid">
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.configFile') }}</span>
            <code>{{ row.status?.state.integrations.find(i => i.capability === 'mcp')?.target_path }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>MCP</span>
            <strong>{{ row.status?.state.integrations.find(i => i.capability === 'mcp')?.status ?? 'unknown' }}</strong>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.command') }}</span>
            <code data-test="mcp-command">{{ row.status?.state.mcp_command || t('settings.mcp.noCommand') }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.agentUrl') }}</span>
            <code data-test="mcp-agent-url">{{ row.status?.state.agent_url || t('settings.mcp.noAgentUrl') }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.skill') }}</span>
            <strong>{{ skillLabel(row.status) }}</strong>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.skillPath') }}</span>
            <code>{{ row.status?.state.integrations.find(i => i.capability === 'skill')?.target_path }}</code>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.hook') }}</span>
            <strong>{{ hookLabel(row.status) }}</strong>
          </div>
          <div class="mcp-detail-item">
            <span>{{ t('settings.mcp.hookPath') }}</span>
            <code>{{ row.status?.state.integrations.find(i => i.capability === 'session_hook')?.target_path }}</code>
          </div>
        </div>
        <div v-if="row.status?.state.message" class="settings-alert settings-alert-warning mcp-inline-alert" data-test="mcp-state-message">
          {{ row.status.state.message }}
        </div>
        <div
          v-if="restartHints[row.id] || row.status.state.requires_restart"
          class="settings-alert settings-alert-warning mcp-inline-alert"
          data-test="mcp-restart-hint"
        >
          {{ t('settings.mcp.restartConnector', { agent: row.label }) }}
        </div>
        <div
          v-if="operationMessage[row.id]"
          class="settings-alert mcp-inline-alert"
          :class="{
            'settings-alert-warning': operationTone[row.id] === 'warning',
            'settings-alert-danger': operationTone[row.id] === 'danger',
          }"
          :data-test="`mcp-operation-message-${row.id}`"
        >
          {{ operationMessage[row.id] }}
        </div>
      </article>

      <button
        v-if="otherBuiltInRows.length > 0"
        class="settings-btn settings-btn-secondary"
        data-test="mcp-toggle-other-builtins"
        type="button"
        :aria-expanded="showOtherBuiltIns"
        @click="showOtherBuiltIns = !showOtherBuiltIns"
      >
        {{ showOtherBuiltIns ? t('settings.mcp.hideOtherBuiltIns') : t('settings.mcp.showOtherBuiltIns') }}
      </button>

      <article class="settings-card" data-test="mcp-generic-manual-card">
        <header class="settings-card-header mcp-card-header">
          <div>
            <h2 class="mcp-agent-name">{{ t('onboarding.manualAgentTitle') }}</h2>
            <p class="mcp-agent-path">{{ t('onboarding.manualAgentDescription') }}</p>
          </div>
          <button class="settings-btn settings-btn-primary" type="button" data-test="mcp-open-generic-manual" @click="manualDialogOpen = true">
            {{ t('settings.mcp.manualConfig') }}
          </button>
        </header>
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
          <pre class="settings-mono mcp-doc-content">{{ manualHint.manual_config }}</pre>
          <p class="mcp-agent-path">{{ t('settings.mcp.configFile') }}: {{ manualHint.config_path ?? t('settings.mcp.noCommand') }}</p>
        </div>
        <footer class="settings-modal-footer">
          <button class="settings-btn settings-btn-primary" type="button" @click="manualHint = null">
            {{ t('settings.mcp.closeManual') }}
          </button>
        </footer>
      </div>
    </div>
  </div>
  <ManualAgentConnectDialog :open="manualDialogOpen" @close="manualDialogOpen = false" @verified="manualDialogOpen = false" />
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

.agent-capabilities {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 0 14px 10px;
}

.agent-capabilities span {
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  padding: 3px 7px;
  font-size: 10px;
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
