<!--
MCP 管理设置页签

职责：
  - 展示 SuperDev MCP 在各编程智能体中的安装状态
  - 触发 MCP/skill 安装更新和卸载
  - 展示 MCP 工具能力说明与 bundled superdev skill 文档
  - 提供机器维度切换：机器选择器首项「本机」走本地路径，其余为已接入的远端
    Host，切换后对远端机器做探测/安装/卸载

边界：
  - 不直接读写 Agent 配置文件，统一通过 api/mcpInstall.ts 调用 Tauri command
  - 不修改 onboarding store
  - 不启动、停止或探测 SuperDev agent 运行态
  - 远端操作经 Tauri command 转发到目标机 agent 的受限文件端点，本组件不感知
    本机 agent 代理链或 nodetransport 转发细节
-->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ask } from '@tauri-apps/plugin-dialog'
import { useI18n } from 'vue-i18n'
import ManualAgentConnectDialog from '@/components/Onboarding/ManualAgentConnectDialog.vue'
import { emitConnectorDiagnostic } from '@/lib/connectorDiagnostics'
import { useAgentsStore } from '@/stores/agents'
import {
  getMcpDocs,
  getAgentConnectorManualInstructions,
  listAgentConnectors,
  installAgentConnector,
  updateAgentConnector,
  verifyAgentConnector,
  uninstallAgentConnector,
  detectRemoteCodingAgents,
  installRemoteAgentConnector,
  uninstallRemoteAgentConnector,
  type ConnectorId,
  type ConnectorManualInstructions,
  type ConnectorOperationOutcome,
  type ConnectorResult,
  type AgentConnectorSummary,
  type McpDocs,
  type McpDocument,
  type RemoteAgentStatus,
} from '@/api/mcpInstall'

const { t } = useI18n()
const agentsStore = useAgentsStore()
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

/**
 * selectedHostId 是机器选择器的当前值：空串 = 本机（走既有本地路径，行为不变），
 * 非空字符串 = 已接入远端 Host 的 host_id。
 *
 * 注意：
 *   - 刻意不用 null 表示本机——`<option :value="null">` 会被 Vue 的 patchDOMProp
 *     当作「无值」整个移除 value 内容属性，届时该 option 的原生 `.value` 会退化
 *     为它的文本内容（"本机"），使浏览器原生 select-by-value（含测试里的
 *     `setValue('')`）都定位不到这个选项。空串是一个真正、稳定的 DOM 属性值，
 *     不会有这个陷阱
 */
const selectedHostId = ref<string>('')
const remoteStatuses = ref<RemoteAgentStatus[]>([])
const remoteLoading = ref(false)
const remoteError = ref('')
const remoteOperationAgent = ref<ConnectorId | null>(null)
const remoteOperationMessage = ref<Record<string, string>>({})
const remoteOperationTone = ref<Record<string, 'success' | 'warning' | 'danger' | 'info'>>({})

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

const isRemoteMode = computed(() => selectedHostId.value !== '')
// remoteHostOptions 复用 agentsStore 已接入节点列表和其现有的 runtime.reachable
// 在线状态字段；本组件不打开 NodeRegistry 订阅，也不引入新的在线态来源。
//
// 注意：
//   - 过滤掉 host_id === '' 的条目——空串是本组件用来表示「本机」的哨兵值
//     （见 selectedHostId 的注释）。真实数据不应出现空 host_id，但一旦出现，
//     不过滤就会静默别名成本机选项，产生第二个「本机」入口
const remoteHostOptions = computed(() =>
  [...agentsStore.agents]
    .filter(agent => agent.host_id !== '')
    .sort((a, b) => a.host_name.localeCompare(b.host_name) || a.host_id.localeCompare(b.host_id)),
)

onMounted(() => {
  void refreshAll()
  void agentsStore.loadAgents()
})

// 切换机器选择器时重新拉取该机器维度的状态；本机（''）与远端共用同一入口，
// 保证「首项本机走现有本地路径」在切换回来时也成立。
//
// 注意：
//   - 切换前必须先清空 remoteStatuses/remoteOperationMessage/remoteOperationTone/
//     remoteOperationAgent。前三者都以 connector_id 为键，不含 host_id；不同机器
//     上完全可能有同名 connector（codex、cursor 都是全局词表）。不清空的话，在
//     新 detect 请求经隧道往返完成前的那段时间——以及旧 outcome 文案本身——都会
//     被误渲染成「新机器」的状态。remoteOperationAgent 同理：不清空会让 host-1
//     上还没落地的 install/uninstall 继续把 host-2 同名 connector 的按钮误判为
//     「正在操作中」而禁用，这正是本任务要杜绝的「把 A 机的事实说成 B 机」
//   - 仅清空这里的快照状态不够——还需要让「切机时正在飞的那次 detect/install/
//     uninstall」在回来后认出自己已经过期、不再落地任何东西。见 refreshRemoteStatus/
//     installRemote/confirmUninstallRemote 里对 `selectedHostId.value !== hostId`
//     的一致校验（乱序 host-1→host-2→host-3 时，先发后至的 host-1 响应必须能被丢弃）
watch(selectedHostId, () => {
  remoteStatuses.value = []
  remoteOperationMessage.value = {}
  remoteOperationTone.value = {}
  remoteOperationAgent.value = null
  void refreshCurrentStatuses()
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
  await Promise.all([refreshCurrentStatuses(), refreshDocs()])
}

/** refreshCurrentStatuses 按机器选择器当前值分派到本机或远端状态刷新。 */
async function refreshCurrentStatuses() {
  if (isRemoteMode.value) {
    await refreshRemoteStatus()
  } else {
    await refreshStatus()
  }
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

/**
 * refreshRemoteStatus 探测当前选中远端机器上的编程智能体接入状态。
 *
 * 注意：
 *   - detect 失败（目标机不可达/本机 agent 代理转发失败等）时把 remoteError 填成
 *     可读文案，而不是留一份空列表——空列表会让用户误以为「这台机器什么都没有」
 *   - 响应落地前必须重新核对 `selectedHostId.value === hostId`：快速连续切换
 *     host-1 → host-2 → host-3 时，host-1 那次 detect 完全可能在切到 host-3
 *     之后才经隧道往返回来。不比对的话，先发后至的响应会把 host-1 的数据/错误
 *     贴到 host-3 的标签下——成功与失败两条路径都要丢弃，把过期的错误说成
 *     当前机器的错误同样是在撒谎。watch(selectedHostId) 里的清空只处理了
 *     「切换那一刻」，处理不了这种飞行中响应乱序落地的情形，两者互补
 */
async function refreshRemoteStatus() {
  const hostId = selectedHostId.value
  if (!hostId) return
  const started = performance.now()
  remoteLoading.value = true
  remoteError.value = ''
  emitConnectorDiagnostic('remote_detect.started', 'info', { surface: 'settings', hostId })
  try {
    const result = await detectRemoteCodingAgents(hostId)
    if (selectedHostId.value !== hostId) {
      emitConnectorDiagnostic('remote_detect.discarded_stale_host', 'info', { surface: 'settings', hostId })
      return
    }
    remoteStatuses.value = result
    emitConnectorDiagnostic('remote_detect.succeeded', 'info', {
      surface: 'settings', hostId, connectorCount: result.length,
      durationMs: Math.round(performance.now() - started),
    })
  } catch (err) {
    if (selectedHostId.value !== hostId) {
      emitConnectorDiagnostic('remote_detect.discarded_stale_host', 'error', { surface: 'settings', hostId })
      return
    }
    remoteStatuses.value = []
    remoteError.value = t('settings.mcpRemote.targetUnreachable', { message: errorMessage(err) })
    emitConnectorDiagnostic('remote_detect.failed', 'error', {
      surface: 'settings', hostId, errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
  } finally {
    if (selectedHostId.value === hostId) {
      remoteLoading.value = false
    }
  }
}

/**
 * remoteInstallLabel 决定安装按钮文案：区分「未安装」与「装了但指向别处」。
 *
 * 注意：
 *   - 只看 mcp_command 是否非空——mcp_installed 为 false 且 mcp_command 有值时，
 *     目标机上确实存在一条 superdev 配置，只是没有指向这台机器自己的 agent，
 *     此时点击按钮的语义是「修正」而不是从零「安装」
 */
function remoteInstallLabel(status: RemoteAgentStatus): string {
  if (!status.mcp_installed && status.mcp_command) return t('settings.mcpRemote.fixPointer')
  return t('settings.mcp.installUpdate')
}

/** remoteActionDisabled 统一 install/uninstall 按钮的禁用判据。 */
function remoteActionDisabled(status: RemoteAgentStatus): boolean {
  return !status.remote_supported || !status.cli_present || remoteOperationAgent.value === status.connector_id
}

/**
 * installRemote 在目标机上安装/修正指向单个远端连接器。
 *
 * 注意：
 *   - 与 refreshRemoteStatus 同一纪律：写回结果前必须核对 `selectedHostId.value
 *     === hostId`，否则用户在等待期间切走机器时，这条结果会被误贴到新机器同名
 *     connector 的行上（remoteOperationMessage/remoteOperationTone 都只按
 *     connector_id 建键，不含 host_id）
 */
async function installRemote(status: RemoteAgentStatus) {
  const hostId = selectedHostId.value
  if (!hostId) return
  const agent = status.connector_id
  const started = performance.now()
  remoteOperationAgent.value = agent
  remoteOperationMessage.value[agent] = ''
  remoteOperationTone.value[agent] = 'info'
  try {
    emitConnectorDiagnostic('remote_install.started', 'info', { surface: 'settings', connectorId: agent, hostId })
    const outcome = await installRemoteAgentConnector(hostId, agent)
    if (selectedHostId.value !== hostId) {
      emitConnectorDiagnostic('remote_install.discarded_stale_host', 'info', { surface: 'settings', connectorId: agent, hostId })
      return
    }
    remoteOperationMessage.value[agent] = formatOutcomeMessage(outcome)
    remoteOperationTone.value[agent] = outcomeTone(outcome.result)
    emitConnectorDiagnostic('remote_install.completed', outcome.result === 'failed' ? 'error' : outcome.result === 'partial' ? 'warn' : 'info', {
      surface: 'settings', connectorId: agent, hostId, result: outcome.result,
      capabilityResults: outcome.integrations.map(item => `${item.capability}=${item.result}`),
      durationMs: Math.round(performance.now() - started),
    })
    await refreshRemoteStatus()
  } catch (err) {
    if (selectedHostId.value !== hostId) {
      emitConnectorDiagnostic('remote_install.discarded_stale_host', 'error', { surface: 'settings', connectorId: agent, hostId })
      return
    }
    remoteOperationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
    remoteOperationTone.value[agent] = 'danger'
    emitConnectorDiagnostic('remote_mutation.failed', 'error', {
      surface: 'settings', connectorId: agent, hostId,
      errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
  } finally {
    if (selectedHostId.value === hostId) {
      remoteOperationAgent.value = null
    }
  }
}

/**
 * confirmUninstallRemote 在目标机上移除单个远端连接器。
 *
 * 注意：
 *   - 同 installRemote 的过期响应纪律：写回结果与调用 refreshRemoteStatus 前都要
 *     核对 `selectedHostId.value === hostId`
 */
async function confirmUninstallRemote(status: RemoteAgentStatus) {
  const hostId = selectedHostId.value
  if (!hostId) return
  const agent = status.connector_id
  const confirmed = await ask(t('settings.mcp.uninstallConfirmMessage', { agent: status.display_name }), {
    title: t('settings.mcp.uninstallConfirmTitle'),
    kind: 'warning',
  })
  if (!confirmed) return
  const started = performance.now()
  remoteOperationAgent.value = agent
  remoteOperationMessage.value[agent] = ''
  remoteOperationTone.value[agent] = 'info'
  try {
    emitConnectorDiagnostic('remote_uninstall.started', 'info', { surface: 'settings', connectorId: agent, hostId })
    const outcome = await uninstallRemoteAgentConnector(hostId, agent)
    if (selectedHostId.value !== hostId) {
      emitConnectorDiagnostic('remote_uninstall.discarded_stale_host', 'info', { surface: 'settings', connectorId: agent, hostId })
      return
    }
    remoteOperationMessage.value[agent] = outcome.result === 'failed'
      ? formatOutcomeMessage(outcome)
      : t('settings.mcp.uninstallDone')
    remoteOperationTone.value[agent] = outcome.result === 'failed' ? 'danger' : 'success'
    emitConnectorDiagnostic('remote_uninstall.completed', outcome.result === 'failed' ? 'error' : 'info', {
      surface: 'settings', connectorId: agent, hostId, result: outcome.result,
      capabilityResults: outcome.integrations.map(item => `${item.capability}=${item.result}`),
      durationMs: Math.round(performance.now() - started),
    })
    await refreshRemoteStatus()
  } catch (err) {
    if (selectedHostId.value !== hostId) {
      emitConnectorDiagnostic('remote_uninstall.discarded_stale_host', 'error', { surface: 'settings', connectorId: agent, hostId })
      return
    }
    remoteOperationMessage.value[agent] = t('settings.mcp.actionFailed', { message: errorMessage(err) })
    remoteOperationTone.value[agent] = 'danger'
    emitConnectorDiagnostic('remote_uninstall.failed', 'error', {
      surface: 'settings', connectorId: agent, hostId,
      errorType: err instanceof Error ? err.name : typeof err,
      durationMs: Math.round(performance.now() - started),
    })
  } finally {
    if (selectedHostId.value === hostId) {
      remoteOperationAgent.value = null
    }
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

    <div class="settings-field mcp-machine-picker">
      <label class="settings-field-label" for="mcp-machine-picker">{{ t('settings.mcpRemote.machinePicker') }}</label>
      <select id="mcp-machine-picker" v-model="selectedHostId" class="settings-select" data-test="mcp-machine-picker">
        <option value="">{{ t('settings.mcpRemote.localMachine') }}</option>
        <option v-for="agent in remoteHostOptions" :key="agent.host_id" :value="agent.host_id">
          {{ agent.host_name }} · {{ agent.runtime.reachable ? t('settings.mcpRemote.online') : t('settings.mcpRemote.offline') }}
        </option>
      </select>
      <p v-if="agentsStore.error" class="settings-alert settings-alert-warning mcp-inline-alert" data-test="mcp-machine-load-error">
        {{ t('settings.mcpRemote.hostsLoadFailed', { message: agentsStore.error }) }}
      </p>
      <p v-else-if="remoteHostOptions.length === 0" class="mcp-agent-path">{{ t('settings.mcpRemote.noRemoteHosts') }}</p>
    </div>

    <template v-if="!isRemoteMode">
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
    </template>
    <template v-else>
      <div v-if="remoteError" class="settings-alert settings-alert-danger" data-test="mcp-remote-error">{{ remoteError }}</div>
      <div v-if="remoteLoading" class="settings-empty" data-test="mcp-remote-loading">{{ t('settings.mcp.loading') }}</div>

      <div class="settings-card-list mcp-agent-list" data-test="mcp-remote-list">
        <article
          v-for="status in remoteStatuses"
          :key="status.connector_id"
          class="settings-card"
          :class="{ 'mcp-remote-row-disabled': !status.remote_supported || !status.cli_present }"
          :data-test="`mcp-remote-row-${status.connector_id}`"
        >
          <header class="settings-card-header mcp-card-header">
            <div>
              <h2 class="mcp-agent-name">{{ status.display_name }}</h2>
              <p class="mcp-agent-path">
                {{ t('settings.mcp.agentStatus') }}:
                {{ status.cli_present ? t('settings.mcp.detected') : t('settings.mcp.notDetected') }}
              </p>
            </div>
            <div class="settings-toolbar">
              <button
                class="settings-btn settings-btn-primary"
                :data-test="`mcp-remote-install-${status.connector_id}`"
                type="button"
                :disabled="remoteActionDisabled(status)"
                @click="installRemote(status)"
              >
                {{ remoteInstallLabel(status) }}
              </button>
              <button
                class="settings-btn settings-btn-danger"
                :data-test="`mcp-remote-uninstall-${status.connector_id}`"
                type="button"
                :disabled="remoteActionDisabled(status)"
                @click="confirmUninstallRemote(status)"
              >
                {{ t('settings.mcp.uninstall') }}
              </button>
            </div>
          </header>

          <!--
            三态互斥，且刻意不共用同一个 v-else 分支：
              1. !remote_supported → 三个状态位整体「查不到」，见 unsupportedNotice
              2. remote_supported 但 !cli_present → detect_remote_agents 对这类行
                 直接返回全 false 占位值、不发一次文件操作（remote_install.rs 的
                 remote_status_for：`if !cli_present { return base }`）。这三个
                 布尔量和 mcp_command/agent_url 在这里和「查不到」是同一件事，
                 必须同样不渲染状态胶囊/详情格，只给出 cliMissing 说明——渲染成
                 「未配置命令」会把「没查」说成「查过、真没有」，是一句假话
              3. remote_supported && cli_present → 三项状态才是读回来的真实值
          -->
          <div
            v-if="!status.remote_supported"
            class="settings-alert settings-alert-warning mcp-inline-alert"
            :data-test="`mcp-remote-unsupported-${status.connector_id}`"
          >
            {{ t('settings.mcpRemote.unsupportedNotice', { agent: status.display_name }) }}
          </div>
          <div
            v-else-if="!status.cli_present"
            class="settings-alert settings-alert-warning mcp-inline-alert"
            :data-test="`mcp-remote-cli-missing-${status.connector_id}`"
          >
            {{ t('settings.mcpRemote.cliMissing', { agent: status.display_name }) }}
          </div>
          <template v-else>
            <div class="agent-capabilities">
              <span :data-test="`mcp-remote-mcp-status-${status.connector_id}`">
                MCP ·
                {{
                  status.mcp_installed
                    ? t('settings.mcp.configured')
                    : (status.mcp_command ? t('settings.mcpRemote.mcpMisdirected') : t('settings.mcp.notConfigured'))
                }}
              </span>
              <span>{{ t('settings.mcp.skill') }} · {{ status.skill_installed ? t('settings.mcp.configured') : t('settings.mcp.notConfigured') }}</span>
              <span>{{ t('settings.mcp.hook') }} · {{ status.hook_installed ? t('settings.mcp.configured') : t('settings.mcp.notConfigured') }}</span>
            </div>
            <div class="mcp-detail-grid">
              <div class="mcp-detail-item">
                <span>{{ t('settings.mcp.command') }}</span>
                <code :data-test="`mcp-remote-command-${status.connector_id}`">{{ status.mcp_command || t('settings.mcp.noCommand') }}</code>
              </div>
              <div class="mcp-detail-item">
                <span>{{ t('settings.mcp.agentUrl') }}</span>
                <code :data-test="`mcp-remote-agent-url-${status.connector_id}`">{{ status.agent_url || t('settings.mcp.noAgentUrl') }}</code>
              </div>
            </div>
            <div
              v-if="status.mcp_command && !status.mcp_installed"
              class="settings-alert settings-alert-warning mcp-inline-alert"
              :data-test="`mcp-remote-misdirected-${status.connector_id}`"
            >
              {{ t('settings.mcpRemote.mcpMisdirectedHint', { command: status.mcp_command, url: status.agent_url || t('settings.mcp.noAgentUrl') }) }}
            </div>
          </template>

          <div
            v-if="remoteOperationMessage[status.connector_id]"
            class="settings-alert mcp-inline-alert"
            :class="{
              'settings-alert-warning': remoteOperationTone[status.connector_id] === 'warning',
              'settings-alert-danger': remoteOperationTone[status.connector_id] === 'danger',
            }"
            :data-test="`mcp-remote-operation-message-${status.connector_id}`"
          >
            {{ remoteOperationMessage[status.connector_id] }}
          </div>
        </article>
      </div>
    </template>

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
.mcp-machine-picker {
  max-width: 320px;
  margin-bottom: 14px;
}

.mcp-remote-row-disabled {
  opacity: 0.55;
}

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
