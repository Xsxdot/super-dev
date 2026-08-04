<!--
AgentConfigPanel：统一管理单台 Host Agent 的监听、安全、安装、连接链和探测。

职责：
  - 将原先分散的安全配置、安装命令、连接链编辑收敛到一个四步面板
  - 保证监听端口、TLS 模式只有一个编辑入口，bind 地址由连接链推导
  - 通过 agents store 调用现有 Agent API 完成保存、下发、生成命令和探测
  - SSH 直推安装遇到「既有 agent 已被其他控制面管理」（409）时，引导用户在
    纳管（发起接入请求 → 等待对方审批 → 兑换凭据 → 落盘 → 连接）与强制重装
    （显式确认后果）之间选择，纳管请求直连目标机地址，不经本机 agent 转发；
    目标地址优先取 409 响应里安装守卫探测到的权威地址，没有时才退化为本机
    已知的 Host IP 信息拼标准端口做尽力而为的猜测（见 adoptionTargetURL）
  - 「等待对方批准」态展示目标机返回的配对码，供发起人念给批准人核对——
    目标机审批列表里可能同时存在多条自报同名的接入请求

边界：
  - 不创建或编辑 Host 身份信息
  - 不直接调用 fetch 或底层 HTTP API（纳管三端点的裸 fetch 封装在 agent.ts，
    本组件只调用 agents store 暴露的方法）
  - 不修改后端 DTO 结构
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentsStore } from '@/stores/agents'
import { agentRouteRows, transportTypeLabelKey } from '@/lib/agentRoute'
import { runtimeFor, type AgentPanelTab } from '@/lib/agentStage'
import {
  bindReasonFromChain,
  directAddressOptions,
  recommendedDirectAddress,
  resolveBindAddressFromChain,
} from '@/lib/agentBind'
import { isExistingAgentDetectedError } from '@/api/agent'
import type {
  AgentConfigUpdatePayload,
  AgentCreatePayload,
  AgentInstallResponse,
  AgentDTO,
  AgentInstallCommandResponse,
  AgentTLSMode,
  AgentTransportUpdatePayload,
  Host,
  NodeStatus,
  ProbeResult,
  TransportEntry,
  TransportType,
} from '@/api/agent'

const props = defineProps<{
  visible: boolean
  agent?: AgentDTO | null
  node?: NodeStatus
  initialTab?: AgentPanelTab
  mode?: 'edit' | 'create'
  hosts?: Host[]
  host?: Host | null
}>()

const emit = defineEmits<{
  cancel: []
  created: [agent: AgentDTO]
}>()

const { t } = useAppI18n()
const agentsStore = useAgentsStore()

const activeTab = ref<AgentPanelTab>('security')
const actionError = ref<string | null>(null)
const savingSecurity = ref(false)
const creatingAgent = ref(false)
const provisioning = ref(false)
const savingTransport = ref(false)
const generatingInstall = ref(false)
const installingPush = ref(false)
const probingAll = ref(false)
const manualAdvancedOpen = ref(false)
const installMode = ref<'generated_command' | 'push_over_ssh'>('generated_command')
const installResult = ref<AgentInstallCommandResponse | null>(null)
const pushInstallResult = ref<AgentInstallResponse | null>(null)
const installStartStatus = ref<'idle' | 'running' | 'success' | 'error'>('idle')
const installSecurityStatus = ref<'idle' | 'waiting' | 'running' | 'success' | 'error'>('idle')
const installStartMessage = ref('')
const installSecurityMessage = ref('')
const checkingGeneratedInstall = ref(false)
const checkingRestart = ref(false)
const restartRequired = ref(false)
const chain = ref<TransportEntry[]>([])
const savedChain = ref<TransportEntry[]>([])
const probeResults = reactive<Record<number, ProbeResult | null>>({})
const hostID = ref('')
const localAgent = ref<AgentDTO | null>(null)
let panelRunID = 0

// ===== 纳管既有 agent（安装 409 existing_agent_detected 分支） =====
// existingAgentDetected 为 true 时，安装 tab 的 phase-start 区域整体切换成
// 「检测到既有 agent」分支（纳管/强制重装二选一），不再展示常规安装内容。
const existingAgentDetected = ref(false)
const existingAgentVersion = ref('')
// existingAgentAddress 是安装守卫 409 响应带回的权威目标机地址（host:port，
// 取自本机为该 Host 配置的 direct 连接链项）——纳管三端点必须用它，不能自己
// 用本控制面当前监听端口配置拼地址（那是两回事，见下方 adoptionTargetURL 注释）。
// 链上只有 tunnel（无 direct 项）时后端返回空字符串，此处同样留空。
const existingAgentAddress = ref('')
const forceReinstallConfirmed = ref(false)
// adoptPhase 只覆盖「发起请求 → 等待批准 → 兑换凭据」这段纳管专属流程；
// 兑换成功后交棒给既有的 installStartStatus/installSecurityStatus 流水线
// （checkConnectedAgent 等），不重复造一套连接确认 UI。
const adoptPhase = ref<'idle' | 'requesting' | 'waiting' | 'exchanging'>('idle')
const adoptFailureMessage = ref('')
// adoptPairingCode 是目标机为本次接入请求派生的配对码：必须展示给发起纳管的人，
// 让他念给对方，对方在审批行上核对同一个码后再批准。
// 它不是秘密（由公开的请求 ID 派生）、也不是鉴权因子，只是防止「同名请求鱼目
// 混珠、批错行」的匹配辅助——真正的准入始终是对方那次人工审批。
const adoptPairingCode = ref('')
const ADOPTION_POLL_INTERVAL_MS = 2000
// 与 requestHeaders() 里桌面端上报给本机 agent 的展示名保持一致的语义：
// 纳管请求同样是「这台桌面在向目标机自报身份」，用同一个可读名称。
const ADOPTION_REQUESTER_NAME = 'SuperDev Desktop'

const securityForm = reactive({
  listenPort: 57017,
  tlsMode: 'auto' as AgentTLSMode,
  serverName: '',
  caCert: '',
})

const installForm = reactive({
  controllerURL: 'http://127.0.0.1:57017',
  releaseBaseURL: __SUPERDEV_RELEASE_BASE_URL__,
  tokenTTLMinutes: 30,
})

const connectCheckRetryAttempts = 45
const connectCheckRetryDelayMS = 2000

const isCreateMode = computed(() => props.mode === 'create')
const selectedHost = computed(() => props.hosts?.find(host => host.id === hostID.value))
const currentHost = computed(() => isCreateMode.value ? selectedHost.value : props.host ?? selectedHost.value)
const pushSSHBlocked = computed(() => {
  const host = currentHost.value
  return !host
    || !host.ssh_host?.trim()
    || !host.ssh_user?.trim()
    || host.ssh_credential_configured !== true
    || host.ssh_host_key_fingerprint_configured !== true
})
const persistedAgent = computed<AgentDTO | null>(() => localAgent.value ?? props.agent ?? null)
const panelAgent = computed<AgentDTO | null>(() => {
  if (persistedAgent.value) return persistedAgent.value
  if (!isCreateMode.value || !selectedHost.value) return null
  return {
    host_id: selectedHost.value.id,
    host_name: selectedHost.value.name,
    tags: selectedHost.value.tags,
    transport: { chain: chain.value.map(normalizeEntry) },
    config: { listen_port: bindPort.value },
    runtime: { installed: false, health: 'unknown', reachable: false },
    security: { token_configured: false, provision_state: 'pending-bootstrap', tls: { mode: securityForm.tlsMode } },
  }
})
const title = computed(() => isCreateMode.value && !persistedAgent.value
  ? t('settings.agents.createTitle')
  : t('settings.agents.panelTitle', { name: panelAgent.value?.host_name ?? '' }))
const runtime = computed(() => persistedAgent.value ? runtimeFor(persistedAgent.value, props.node) : undefined)
const canProbe = computed(() => runtime.value?.installed === true)
const bindPort = computed(() => Number(securityForm.listenPort) || 57017)
const bindAddress = computed(() => resolveBindAddressFromChain(chain.value))
const bindReason = computed(() => bindReasonFromChain(chain.value))
const bindReasonKey = computed(() => bindReason.value === 'direct' ? 'settings.agents.bindReasonDirect' : 'settings.agents.bindReasonLoopback')
const directOptions = computed(() => directAddressOptions(currentHost.value, bindPort.value))
// STANDARD_AGENT_PORT 是 SuperDev agent 的标准默认监听端口，仅用于纳管地址的
// 兜底猜测（守卫没能给出权威地址时）。刻意不用 bindPort/securityForm.listenPort
// ——那是本控制面这次准备安装的新 agent 打算监听的端口，与目标机既有 agent
// 实际监听的端口毫无关系，用它拼地址是纯粹的误导（review Important 2）。
const STANDARD_AGENT_PORT = 57017

// adoptionTargetURL 是纳管三端点直连的目标机地址。
//
// 优先级：
//   1. existingAgentAddress——安装守卫 409 响应带回的权威地址，取自本机为该
//      Host 配置的 direct 连接链项，是"目标机真实监听地址"最可信的来源
//   2. 兜底：Host 记录里已知的 public_ip/private_ip/ssh_host 拼标准默认端口
//      ——仅在守卫没能给出权威地址时使用（例如连接链是纯 tunnel，没有 direct
//      项可读），是尽力而为的猜测，不保证命中，命中失败会在纳管请求失败态
//      可视化，不会静默
const adoptionTargetURL = computed(() => {
  const authoritative = existingAgentAddress.value.trim()
  if (authoritative) return `http://${authoritative}`
  const guessed = recommendedDirectAddress(currentHost.value, STANDARD_AGENT_PORT)
  return guessed ? `http://${guessed}` : ''
})
const hasPublicIP = computed(() => Boolean(currentHost.value?.public_ip?.trim()))
const installTransportType = computed(() => chain.value[0]?.type ?? 'tunnel')
const transportDirty = computed(() => chainSignature(chain.value) !== chainSignature(savedChain.value))
const tunnelTargetDirty = computed(() => Boolean(persistedAgent.value)
  && tunnelTargetSignature(chain.value) !== tunnelTargetSignature(savedChain.value))
const bindScopeDirty = computed(() => (
  runtime.value?.installed === true &&
  resolveBindAddressFromChain(savedChain.value) !== resolveBindAddressFromChain(chain.value)
))
const currentRows = computed(() => panelAgent.value
  ? agentRouteRows({ ...panelAgent.value, transport: { chain: chain.value } }, transportDirty.value ? undefined : props.node)
  : [])
const needsCreateBeforeNextStep = computed(() => isCreateMode.value && !persistedAgent.value)
const pendingManualRestart = computed(() => (
  restartRequired.value &&
  installMode.value === 'generated_command' &&
  installSecurityStatus.value === 'waiting'
))
const canRetryInstallSecurityCheck = computed(() => (
  installSecurityStatus.value === 'error' &&
  installStartStatus.value === 'success' &&
  Boolean(persistedAgent.value)
))

const tabs = computed<Array<{ key: AgentPanelTab; label: string; locked: boolean; done: boolean }>>(() => [
  { key: 'security', label: t('settings.agents.tabSecurity'), locked: false, done: Boolean(persistedAgent.value?.config?.listen_port) },
  { key: 'transport', label: t('settings.agents.tabTransport'), locked: false, done: Boolean(persistedAgent.value?.transport?.chain?.length) },
  { key: 'install', label: t('settings.agents.tabInstall'), locked: needsCreateBeforeNextStep.value, done: runtime.value?.installed === true },
  { key: 'probe', label: t('settings.agents.tabProbe'), locked: !canProbe.value, done: runtime.value?.health === 'healthy' },
])

function defaultEntry(type: TransportType): TransportEntry {
  if (type === 'tunnel') return { type, tunnel: { remote_agent_port: bindPort.value } }
  if (type === 'direct') return { type, direct: { address: recommendedDirectAddress(currentHost.value, bindPort.value) } }
  return { type }
}

function normalizeEntry(entry: TransportEntry): TransportEntry {
  if (entry.type === 'tunnel') {
    return { type: 'tunnel', tunnel: { remote_agent_port: bindPort.value } }
  }
  if (entry.type === 'direct') {
    return { type: 'direct', direct: { address: entry.direct?.address?.trim() ?? '' } }
  }
  return { type: entry.type }
}

function cloneChain(agent?: AgentDTO | null, fallbackType: TransportType = 'direct'): TransportEntry[] {
  const source = agent?.transport?.chain?.length ? agent.transport.chain : [defaultEntry(fallbackType)]
  return source.map(normalizeEntry)
}

function chainSignature(entries: TransportEntry[]): string {
  return JSON.stringify(entries.map(normalizeEntry))
}

function tunnelTargetSignature(entries: TransportEntry[]): string {
  const tunnel = entries.find(entry => entry.type === 'tunnel' && entry.tunnel)
  // 后端只在 tunnel 是否存在或远端 Agent 端口变化时撤销旧转发；direct 编辑不应误报警告。
  return tunnel ? `tunnel:${Number(tunnel.tunnel?.remote_agent_port) || 57017}` : 'no-tunnel'
}

function clearProbeResults() {
  Object.keys(probeResults).forEach(key => delete probeResults[Number(key)])
}

function invalidatePanelRun() {
  panelRunID += 1
}

function isPanelRunActive(runID: number) {
  return props.visible && panelRunID === runID
}

function clearBusyState() {
  savingSecurity.value = false
  creatingAgent.value = false
  provisioning.value = false
  savingTransport.value = false
  generatingInstall.value = false
  installingPush.value = false
  probingAll.value = false
  checkingGeneratedInstall.value = false
  checkingRestart.value = false
}

function requestClose() {
  invalidatePanelRun()
  clearBusyState()
  emit('cancel')
}

function syncDefaultCreateTunnelPort() {
  if (!needsCreateBeforeNextStep.value || transportDirty.value) return
  const normalizedChain = chain.value.map(normalizeEntry)
  if (normalizedChain.length !== 1 || normalizedChain[0]?.type !== 'tunnel') return
  const nextChain: TransportEntry[] = [{ type: 'tunnel', tunnel: { remote_agent_port: bindPort.value } }]
  chain.value = nextChain
  savedChain.value = nextChain.map(normalizeEntry)
}

function reset(agent?: AgentDTO | null) {
  invalidatePanelRun()
  clearBusyState()
  localAgent.value = agent ?? null
  activeTab.value = props.initialTab ?? 'security'
  actionError.value = null
  manualAdvancedOpen.value = false
  installMode.value = 'generated_command'
  installResult.value = null
  pushInstallResult.value = null
  resetInstallPhases()
  hostID.value = agent?.host_id ?? props.hosts?.[0]?.id ?? ''
  securityForm.listenPort = agent?.config?.listen_port || 57017
  const mode = agent?.security?.tls?.mode
  securityForm.tlsMode = mode === 'off' || mode === 'manual' ? mode : 'auto'
  securityForm.serverName = agent?.security?.tls?.server_name ?? ''
  securityForm.caCert = agent?.security?.tls?.ca_cert ?? ''
  const nextChain = cloneChain(agent, isCreateMode.value ? 'tunnel' : 'direct')
  chain.value = nextChain
  savedChain.value = nextChain.map(normalizeEntry)
  clearProbeResults()
}

function selectTab(tab: AgentPanelTab) {
  if (tab === 'transport') {
    syncDefaultCreateTunnelPort()
  }
  activeTab.value = tab
  actionError.value = null
}

function securityPayload(): AgentConfigUpdatePayload {
  const agent = persistedAgent.value
  const tls = tlsPayload()
  return {
    config: {
      listen_port: bindPort.value,
    },
    security: {
      token_configured: Boolean(agent?.security?.token_configured),
      provision_state: agent?.security?.provision_state || 'pending-bootstrap',
      tls,
    },
  }
}

function tlsPayload(): AgentConfigUpdatePayload['security']['tls'] {
  const tls: AgentConfigUpdatePayload['security']['tls'] = { mode: securityForm.tlsMode }
  if (securityForm.tlsMode === 'manual') {
    if (securityForm.serverName.trim()) tls.server_name = securityForm.serverName.trim()
    if (securityForm.caCert.trim()) tls.ca_cert = securityForm.caCert
  }
  return tls
}

function createPayload(): AgentCreatePayload {
  return {
    host_id: hostID.value,
    transport: { chain: chain.value.map(normalizeEntry) },
    config: {
      listen_port: bindPort.value,
    },
    security: {
      token_configured: false,
      provision_state: 'pending-bootstrap',
      tls: tlsPayload(),
    },
  }
}

async function saveSecurity() {
  if (needsCreateBeforeNextStep.value) {
    if (!hostID.value) return
    actionError.value = null
    syncDefaultCreateTunnelPort()
    activeTab.value = 'transport'
    return
  }
  const agent = persistedAgent.value
  if (!agent) return
  savingSecurity.value = true
  actionError.value = null
  try {
    localAgent.value = await agentsStore.updateAgentConfig(agent.host_id, securityPayload())
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    savingSecurity.value = false
  }
}

async function provisionSecurity() {
  const agent = persistedAgent.value
  if (!agent) return
  provisioning.value = true
  actionError.value = null
  try {
    await agentsStore.provisionAgent(agent.host_id, { index: 0, tls_mode: securityForm.tlsMode })
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    provisioning.value = false
  }
}

async function generateInstallCommand() {
  const agent = persistedAgent.value
  if (!agent) return
  generatingInstall.value = true
  actionError.value = null
  installResult.value = null
  resetInstallPhases()
  try {
    installResult.value = await agentsStore.generateInstallCommand(agent.host_id, {
      method: 'generated_command',
      controller_url: installForm.controllerURL.trim(),
      release_base_url: installForm.releaseBaseURL.trim() || undefined,
      remote_agent_port: bindPort.value,
      transport_type: installTransportType.value,
      token_ttl_minutes: Number(installForm.tokenTTLMinutes) || 30,
    })
    installStartMessage.value = t('settings.agents.installCommandWaiting')
    installSecurityStatus.value = 'waiting'
    installSecurityMessage.value = t('settings.agents.installSecurityWaitingForCommand')
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    generatingInstall.value = false
  }
}

async function copyCommand() {
  if (!installResult.value?.command) return
  await navigator.clipboard?.writeText(installResult.value.command)
}

async function copyRestartCommand() {
  if (!installResult.value?.restart_command) return
  await navigator.clipboard?.writeText(installResult.value.restart_command)
}

function addEntry(type: TransportType) {
  chain.value = [...chain.value, defaultEntry(type)]
}

function applyDirectAddressOption(index: number, address: string) {
  const entry = chain.value[index]
  if (!entry || entry.type !== 'direct') return
  entry.direct = { address }
}

function removeEntry(index: number) {
  if (chain.value.length <= 1) return
  chain.value = chain.value.filter((_entry, i) => i !== index)
}

function moveEntry(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= chain.value.length) return
  const next = [...chain.value]
  const [item] = next.splice(index, 1)
  next.splice(target, 0, item)
  chain.value = next
}

async function testEntry(index: number) {
  const agent = persistedAgent.value
  if (!agent || transportDirty.value) return
  actionError.value = null
  try {
    probeResults[index] = await agentsStore.testTransport(agent.host_id, index)
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  }
}

async function saveTransport() {
  actionError.value = null
  const normalizedChain = chain.value.map(normalizeEntry)
  if (needsCreateBeforeNextStep.value) {
    if (!hostID.value) return
    creatingAgent.value = true
    try {
      chain.value = normalizedChain.map(normalizeEntry)
      savedChain.value = normalizedChain.map(normalizeEntry)
      const created = await agentsStore.createAgent(createPayload())
      localAgent.value = created
      activeTab.value = 'install'
      emit('created', created)
    } catch (err) {
      actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
    } finally {
      creatingAgent.value = false
    }
    return
  }
  const agent = persistedAgent.value
  if (!agent) return
  savingTransport.value = true
  const payload: AgentTransportUpdatePayload = { transport: { chain: normalizedChain } }
  try {
    localAgent.value = await agentsStore.updateAgentTransport(agent.host_id, payload)
    chain.value = normalizedChain.map(normalizeEntry)
    savedChain.value = normalizedChain.map(normalizeEntry)
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    savingTransport.value = false
  }
}

function resetInstallPhases() {
  installStartStatus.value = 'idle'
  installSecurityStatus.value = 'idle'
  installStartMessage.value = ''
  installSecurityMessage.value = ''
  checkingGeneratedInstall.value = false
  checkingRestart.value = false
  restartRequired.value = false
  existingAgentDetected.value = false
  existingAgentVersion.value = ''
  existingAgentAddress.value = ''
  forceReinstallConfirmed.value = false
  adoptPhase.value = 'idle'
  adoptFailureMessage.value = ''
}

function firstProvisionIndex(): number {
  return 0
}

async function provisionAndConnect(runID = panelRunID) {
  const agent = persistedAgent.value
  if (!agent) return
  restartRequired.value = false
  installSecurityStatus.value = 'running'
  installSecurityMessage.value = t('settings.agents.installSecurityRunning')
  const provision = await agentsStore.provisionAgent(agent.host_id, { index: firstProvisionIndex(), tls_mode: securityForm.tlsMode })
  if (!isPanelRunActive(runID)) return
  if (provision.restart_required) {
    restartRequired.value = true
    localAgent.value = agentsStore.agentOf(agent.host_id) ?? localAgent.value
    if (installMode.value === 'push_over_ssh') {
      await restartAndConnect(agent.host_id, runID)
      return
    }
    installSecurityStatus.value = 'waiting'
    installSecurityMessage.value = t('settings.agents.installSecurityRestartRequired')
    return
  }
  await checkConnectedAgent(agent.host_id, false, 'settings.agents.installStartCheckFailed', false, runID)
}

async function restartAndConnect(hostId: string, runID = panelRunID) {
  installSecurityStatus.value = 'running'
  installSecurityMessage.value = t('settings.agents.installSecurityRestarting')
  await agentsStore.restartAgent(hostId)
  if (!isPanelRunActive(runID)) return
  await checkConnectedAgent(hostId, true, 'settings.agents.installRestartCheckFailed', true, runID)
}

async function sleep(ms: number) {
  await new Promise(resolve => globalThis.setTimeout(resolve, ms))
}

async function checkConnectedAgent(hostId: string, goProbe: boolean, notInstalledMessageKey: string, retry = false, runID = panelRunID) {
  const total = retry ? connectCheckRetryAttempts : 1
  let lastError: unknown = null
  for (let attempt = 1; attempt <= total; attempt += 1) {
    if (!isPanelRunActive(runID)) return
    if (retry) {
      installSecurityMessage.value = t('settings.agents.installSecurityRetrying', { attempt, total })
    }
    try {
      const checked = await agentsStore.checkAgent(hostId)
      if (!isPanelRunActive(runID)) return
      localAgent.value = checked
      if (checked.runtime.installed) {
        installSecurityStatus.value = 'success'
        installSecurityMessage.value = t('settings.agents.installConnected')
        restartRequired.value = false
        if (goProbe) activeTab.value = 'probe'
        return
      }
      lastError = new Error(t(notInstalledMessageKey))
    } catch (err) {
      lastError = err
    }
    if (attempt < total) {
      // systemd/launchd 返回已重启时，TLS listener 和隧道状态仍可能需要几秒收敛。
      await sleep(connectCheckRetryDelayMS)
      if (!isPanelRunActive(runID)) return
    }
  }
  throw lastError instanceof Error ? lastError : new Error(t(notInstalledMessageKey))
}

async function confirmAgentRestarted() {
  await retryInstallSecurityCheck()
}

async function retryInstallSecurityCheck() {
  const agent = persistedAgent.value
  if (!agent) return
  const runID = panelRunID
  checkingRestart.value = true
  actionError.value = null
  installSecurityStatus.value = 'running'
  installSecurityMessage.value = t('settings.agents.installSecurityCheckingAfterRestart')
  try {
    await checkConnectedAgent(agent.host_id, true, 'settings.agents.installRestartCheckFailed', true, runID)
  } catch (err) {
    if (!isPanelRunActive(runID)) return
    installSecurityStatus.value = 'error'
    installSecurityMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    if (isPanelRunActive(runID)) checkingRestart.value = false
  }
}

async function confirmGeneratedInstallExecuted() {
  const agent = persistedAgent.value
  if (!agent) return
  const runID = panelRunID
  checkingGeneratedInstall.value = true
  actionError.value = null
  installStartStatus.value = 'running'
  installStartMessage.value = t('settings.agents.installCheckingStarted')
  installSecurityStatus.value = 'waiting'
  installSecurityMessage.value = t('settings.agents.installSecurityWaitingForStart')
  try {
    const checked = await agentsStore.checkAgent(agent.host_id)
    if (!isPanelRunActive(runID)) return
    localAgent.value = checked
    if (!checked.runtime.installed) {
      throw new Error(t('settings.agents.installStartCheckFailed'))
    }
    installStartStatus.value = 'success'
    installStartMessage.value = t('settings.agents.installStarted')
    await provisionAndConnect(runID)
  } catch (err) {
    if (!isPanelRunActive(runID)) return
    if (installStartStatus.value !== 'success') {
      installStartStatus.value = 'error'
      installStartMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
    } else {
      installSecurityStatus.value = 'error'
      installSecurityMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
    }
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    if (isPanelRunActive(runID)) checkingGeneratedInstall.value = false
  }
}

// pushInstall 发起 SSH 直推安装。
//
// 参数：
//   - forceReinstall: 用户在「检测到既有 agent」分支显式勾选确认后传 true，
//     跳过后端探测守卫盲目重装；省略/false 为常规首装路径
//
// 注意：
//   - 模板绑定必须写成 pushInstall() / pushInstall(true) 的显式调用形式，
//     不能写裸方法引用 @click="pushInstall"——Vue 会把原生 MouseEvent 当成
//     第一个参数传入，等价于每次点击都被强制解读成 forceReinstall=true
async function pushInstall(forceReinstall = false) {
  const agent = persistedAgent.value
  // Push 安装必须与后端 fail-closed 的 SSH 身份合同保持一致，不能先发起必然失败的远程操作。
  if (!agent || pushSSHBlocked.value) return
  const runID = panelRunID
  installingPush.value = true
  actionError.value = null
  pushInstallResult.value = null
  resetInstallPhases()
  installStartStatus.value = 'running'
  installStartMessage.value = t('settings.agents.installStartRunning')
  installSecurityStatus.value = 'waiting'
  installSecurityMessage.value = t('settings.agents.installSecurityWaitingForStart')
  try {
    pushInstallResult.value = await agentsStore.installAgent(agent.host_id, { method: 'push_over_ssh', force_reinstall: forceReinstall })
    if (!isPanelRunActive(runID)) return
    installStartStatus.value = 'success'
    installStartMessage.value = t('settings.agents.installStarted')
    await provisionAndConnect(runID)
  } catch (err) {
    if (!isPanelRunActive(runID)) return
    if (isExistingAgentDetectedError(err)) {
      // 守卫拦截：不是普通安装失败，是需要用户在纳管/强制重装之间做选择——
      // 清空常规安装态（避免同时展示"安装出错"和"检测到既有 agent"两套矛盾文案），
      // 转到既有 agent 检测分支。
      installStartStatus.value = 'idle'
      installStartMessage.value = ''
      installSecurityStatus.value = 'idle'
      installSecurityMessage.value = ''
      existingAgentDetected.value = true
      existingAgentVersion.value = err.version ?? ''
      existingAgentAddress.value = err.address ?? ''
      return
    }
    if (installStartStatus.value !== 'success') {
      installStartStatus.value = 'error'
      installStartMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
    } else {
      installSecurityStatus.value = 'error'
      installSecurityMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
    }
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    if (isPanelRunActive(runID)) installingPush.value = false
  }
}

// startAdoption 发起一次纳管流程：Create → 2s 轮询 → approved 自动 Exchange →
// 凭据落盘 → 走既有 connect 流程（checkConnectedAgent）。
//
// 注意：
//   - 全程用 isPanelRunActive(runID) 做终止判定，与面板其余异步链路同一套
//     机制——用户取消（cancelAdoption）、面板关闭/重置（reset/requestClose）
//     都会推进 panelRunID，让本函数的后续步骤在下一次检查点自然退出
//   - TTL 终止条件不完全依赖服务端：expires_at 在客户端也做一次绝对时间判断，
//     防止服务端因为某种原因迟迟不把 state 翻成 expired 时轮询永动
async function startAdoption() {
  const agent = persistedAgent.value
  if (!agent) return
  if (!adoptionTargetURL.value) {
    adoptFailureMessage.value = t('agentInstall.adoptTargetUnknown')
    return
  }
  const runID = panelRunID
  adoptFailureMessage.value = ''
  adoptPairingCode.value = ''
  adoptPhase.value = 'requesting'
  try {
    const created = await agentsStore.requestAdoption(adoptionTargetURL.value, ADOPTION_REQUESTER_NAME)
    if (!isPanelRunActive(runID)) return
    adoptPairingCode.value = created.pairing_code ?? ''
    adoptPhase.value = 'waiting'
    await pollAdoptionStatus(agent.host_id, created.id, created.expires_at, runID)
  } catch (err) {
    if (!isPanelRunActive(runID)) return
    adoptPhase.value = 'idle'
    adoptFailureMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
  }
}

// pollAdoptionStatus 每 2 秒查询一次接入请求状态，直到进入终态或被取消/关闭/超时。
async function pollAdoptionStatus(hostId: string, requestId: string, expiresAt: string, runID: number) {
  const parsedDeadline = Date.parse(expiresAt)
  const deadline = Number.isFinite(parsedDeadline) ? parsedDeadline : Number.POSITIVE_INFINITY
  for (;;) {
    if (!isPanelRunActive(runID)) return
    if (Date.now() >= deadline) {
      adoptPhase.value = 'idle'
      adoptFailureMessage.value = t('agentInstall.adoptExpired')
      return
    }
    let status
    try {
      status = await agentsStore.getAdoptionStatus(adoptionTargetURL.value, requestId)
    } catch (err) {
      if (!isPanelRunActive(runID)) return
      adoptPhase.value = 'idle'
      adoptFailureMessage.value = err instanceof Error ? err.message : t('common.requestFailed')
      return
    }
    if (!isPanelRunActive(runID)) return
    if (status.state === 'approved') {
      if (!status.adoption_token) {
        // 一次性 token 已在别处被领取（例如面板曾经关闭又重新打开过一轮）——
        // 没有 token 就无法兑换，只能引导用户重新发起，不能在这里假装成功。
        adoptPhase.value = 'idle'
        adoptFailureMessage.value = t('agentInstall.adoptTokenLost')
        return
      }
      await completeAdoption(hostId, requestId, status.adoption_token, runID)
      return
    }
    if (status.state === 'rejected') {
      adoptPhase.value = 'idle'
      adoptFailureMessage.value = t('agentInstall.adoptRejected')
      return
    }
    if (status.state === 'expired') {
      adoptPhase.value = 'idle'
      adoptFailureMessage.value = t('agentInstall.adoptExpired')
      return
    }
    // pending：继续轮询。
    await sleep(ADOPTION_POLL_INTERVAL_MS)
    if (!isPanelRunActive(runID)) return
  }
}

// completeAdoption 用一次性 adoption_token 兑换长期凭据、落盘到本机 agents.json，
// 再走既有 checkConnectedAgent 连接确认流程（同 provisionAndConnect 尾段）。
async function completeAdoption(hostId: string, requestId: string, adoptionToken: string, runID: number) {
  adoptPhase.value = 'exchanging'
  try {
    const exchanged = await agentsStore.exchangeAdoption(adoptionTargetURL.value, requestId, adoptionToken)
    if (!isPanelRunActive(runID)) return
    await agentsStore.adoptAgentCredential(hostId, exchanged.token)
    if (!isPanelRunActive(runID)) return
    // 凭据已落盘：纳管流程本身完成，切回既有安装阶段流水线展示连接确认进度，
    // 复用它已有的失败/重试 UI（agent-install-security-retry），不重复造一套。
    existingAgentDetected.value = false
    adoptPhase.value = 'idle'
    adoptFailureMessage.value = ''
    adoptPairingCode.value = ''
    installStartStatus.value = 'success'
    installStartMessage.value = t('settings.agents.installStarted')
    installSecurityStatus.value = 'running'
    installSecurityMessage.value = t('settings.agents.installSecurityRunning')
    await checkConnectedAgent(hostId, true, 'settings.agents.installRestartCheckFailed', true, runID)
  } catch (err) {
    if (!isPanelRunActive(runID)) return
    const message = err instanceof Error ? err.message : t('common.requestFailed')
    if (existingAgentDetected.value) {
      // 还没切回既有流水线（exchange 或 adoptAgentCredential 本身失败）：
      // 留在纳管分支报错，不能悄悄回到「安装并启动」的常规文案。
      adoptPhase.value = 'idle'
      adoptFailureMessage.value = message
    } else {
      // 凭据已落盘，只是随后的连接确认失败：交给既有安装阶段的错误态和重试按钮。
      installSecurityStatus.value = 'error'
      installSecurityMessage.value = message
    }
    actionError.value = message
  }
}

// cancelAdoption 停止纳管轮询并回到「检测到既有 agent」的初始选择态。
//
// 注意：
//   - invalidatePanelRun 让所有仍在途的 isPanelRunActive(runID) 检查在下一个
//     检查点失败退出，是本组件唯一的后台异步终止机制，纳管轮询复用它，
//     不需要额外的 AbortController
function cancelAdoption() {
  invalidatePanelRun()
  adoptPhase.value = 'idle'
  adoptFailureMessage.value = ''
  adoptPairingCode.value = ''
}

async function probeAll() {
  const agent = persistedAgent.value
  if (!agent || !canProbe.value || transportDirty.value) return
  probingAll.value = true
  actionError.value = null
  clearProbeResults()
  try {
    for (let index = 0; index < chain.value.length; index += 1) {
      probeResults[index] = await agentsStore.testTransport(agent.host_id, index)
    }
    await agentsStore.checkAgent(agent.host_id)
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    probingAll.value = false
  }
}

function probeLabel(result?: ProbeResult | null): string {
  if (!result) return t('settings.agents.probeUntested')
  const latency = typeof result.latency_ms === 'number' ? ` · ${result.latency_ms}ms` : ''
  const error = result.error ? ` · ${result.error}` : ''
  return `${result.status}${latency}${error}`
}

function transportLabel(type?: string): string {
  return t(transportTypeLabelKey(type))
}

watch(
  () => [props.visible, props.agent, props.initialTab] as const,
  ([visible, agent]) => {
    if (visible) reset(agent)
    else {
      invalidatePanelRun()
      clearBusyState()
    }
  },
  { immediate: true },
)

watch(
  chain,
  () => {
    if (transportDirty.value) clearProbeResults()
  },
  { deep: true },
)

onBeforeUnmount(() => {
  invalidatePanelRun()
  clearBusyState()
})
</script>

<template>
  <div v-if="visible && (persistedAgent || isCreateMode)" class="settings-modal-backdrop" @click.self="requestClose">
    <section class="settings-modal settings-modal-wide agent-config-panel" data-test="agent-panel">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ title }}</h2>
        <button type="button" class="settings-btn settings-btn-ghost" @click="requestClose">{{ t('common.close') }}</button>
      </header>

      <div class="panel-layout">
        <nav class="panel-tabs" aria-label="Agent configuration steps">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            class="panel-tab"
            :class="{ active: activeTab === tab.key, locked: tab.locked }"
            :data-test="`agent-panel-tab-${tab.key}`"
            @click="selectTab(tab.key)"
          >
            <span class="tab-state" aria-hidden="true">{{ tab.locked ? '!' : (tab.done ? '✓' : '') }}</span>
            <span>{{ tab.label }}</span>
          </button>
        </nav>

        <main class="settings-modal-body panel-body">
          <div v-if="actionError" class="settings-alert settings-alert-danger">{{ actionError }}</div>

          <section v-if="activeTab === 'security'" class="panel-step">
            <div v-if="needsCreateBeforeNextStep" class="settings-field">
              <label class="settings-field-label">{{ t('settings.agents.createHost') }}</label>
              <select v-model="hostID" class="settings-select" data-test="agent-create-host">
                <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
              </select>
            </div>
            <div class="row">
              <div class="settings-field port">
                <label class="settings-field-label">{{ t('settings.agents.remotePort') }}</label>
                <input v-model.number="securityForm.listenPort" class="settings-input" type="number" min="1" data-test="agent-listen-port" />
              </div>
            </div>
            <div v-if="hasPublicIP" class="settings-alert settings-alert-warning" data-test="agent-public-ip-tls-hint">
              {{ t('settings.agents.publicIPTLSHint') }}
            </div>

            <div class="settings-field">
              <label class="settings-field-label">{{ t('settings.agents.tlsMode') }}</label>
              <div class="segmented">
                <label><input v-model="securityForm.tlsMode" type="radio" value="off" data-test="agent-tls-mode-off" /> {{ t('settings.agents.tlsOff') }}</label>
                <label><input v-model="securityForm.tlsMode" type="radio" value="auto" data-test="agent-tls-mode-auto" /> {{ t('settings.agents.tlsAuto') }}</label>
                <label><input v-model="securityForm.tlsMode" type="radio" value="manual" data-test="agent-tls-mode-manual" /> {{ t('settings.agents.tlsManual') }}</label>
              </div>
            </div>

            <button
              v-if="securityForm.tlsMode === 'manual'"
              type="button"
              class="settings-btn settings-btn-secondary"
              data-test="agent-manual-advanced-toggle"
              @click="manualAdvancedOpen = !manualAdvancedOpen"
            >
              {{ manualAdvancedOpen ? t('settings.agents.hideManualTLS') : t('settings.agents.showManualTLS') }}
            </button>

            <div v-if="securityForm.tlsMode === 'manual' && manualAdvancedOpen" class="manual-tls">
              <div class="settings-field">
                <label class="settings-field-label">{{ t('settings.agents.sniName') }}</label>
                <input v-model="securityForm.serverName" class="settings-input" data-test="agent-server-name" />
                <span class="field-hint">{{ t('settings.agents.sniNameHint') }}</span>
              </div>
              <div class="settings-field">
                <label class="settings-field-label">{{ t('settings.agents.caCert') }}</label>
                <textarea v-model="securityForm.caCert" class="settings-input cert-box" data-test="agent-ca-cert" />
              </div>
            </div>

            <div v-if="persistedAgent" class="security-status">
              <span>{{ t('settings.agents.provisionState') }}: {{ persistedAgent.security.provision_state }}</span>
              <span>{{ t('settings.agents.tokenConfigured') }}: {{ persistedAgent.security.token_configured ? t('common.yes') : t('common.no') }}</span>
            </div>
            <footer class="step-actions">
              <button v-if="persistedAgent" type="button" class="settings-btn settings-btn-secondary" :disabled="provisioning" data-test="agent-provision-security" @click="provisionSecurity">
                {{ provisioning ? t('common.loading') : t('settings.agents.provisionSecurity') }}
              </button>
              <button type="button" class="settings-btn settings-btn-primary" :disabled="savingSecurity || (needsCreateBeforeNextStep && !hostID)" data-test="agent-security-save" @click="saveSecurity">
                {{ savingSecurity ? t('common.loading') : t(needsCreateBeforeNextStep ? 'settings.agents.next' : 'common.save') }}
              </button>
            </footer>
          </section>

          <section v-if="activeTab === 'install'" class="panel-step">
            <div v-if="needsCreateBeforeNextStep" class="settings-alert settings-alert-warning" data-test="agent-create-before-install">
              {{ t('settings.agents.createBeforeNextStep') }}
            </div>
            <template v-else>
              <div class="settings-field">
                <label class="settings-field-label">{{ t('settings.agents.installMethod') }}</label>
                <div class="segmented">
                  <label><input v-model="installMode" type="radio" value="generated_command" /> {{ t('settings.agents.generatedCommand') }}</label>
                  <label><input v-model="installMode" type="radio" value="push_over_ssh" /> {{ t('settings.agents.pushOverSSH') }}</label>
                </div>
              </div>

              <div class="readonly-chips">
                <span class="settings-badge" data-test="agent-install-bind-preview">{{ t('settings.agents.installBindPreview', { address: bindAddress, port: bindPort }) }}</span>
                <span class="settings-badge">{{ t('settings.agents.installTLSPreview', { mode: securityForm.tlsMode }) }}</span>
                <span class="settings-badge">{{ t('settings.agents.installTransportPreview', { type: installTransportType }) }}</span>
              </div>
              <p class="step-note" data-test="agent-bind-reason">{{ t(bindReasonKey) }}</p>

              <div class="install-phases">
                <!-- 安装守卫 409：整段 phase-start 换成「检测到既有 agent」分支，纳管/强制重装二选一。 -->
                <section v-if="existingAgentDetected" class="install-phase phase-error" data-test="install-phase-start">
                  <div class="settings-alert settings-alert-warning" data-test="agent-install-existing-detected">
                    {{ t('agentInstall.existingDetected', { version: existingAgentVersion || '?' }) }}
                  </div>

                  <template v-if="adoptPhase === 'idle'">
                    <p v-if="adoptFailureMessage" class="install-note warning" data-test="agent-adopt-failed">{{ adoptFailureMessage }}</p>

                    <div class="step-actions step-actions-left">
                      <button class="settings-btn settings-btn-primary" type="button" data-test="agent-adopt-start" @click="startAdoption">
                        {{ t('agentInstall.adopt') }}
                      </button>
                    </div>

                    <div class="force-reinstall-block">
                      <label class="force-reinstall-consent">
                        <input v-model="forceReinstallConfirmed" type="checkbox" data-test="agent-force-reinstall-confirm" />
                        {{ t('agentInstall.forceReinstallConfirm') }}
                      </label>
                      <div v-if="forceReinstallConfirmed" class="settings-alert settings-alert-danger" data-test="agent-force-reinstall-warning">
                        {{ t('agentInstall.forceReinstallWarning') }}
                      </div>
                      <button
                        class="settings-btn settings-btn-danger"
                        type="button"
                        :disabled="!forceReinstallConfirmed || installingPush"
                        data-test="agent-force-reinstall"
                        @click="pushInstall(true)"
                      >
                        {{ t('agentInstall.forceReinstall') }}
                      </button>
                    </div>
                  </template>

                  <template v-else>
                    <p class="step-note" data-test="agent-adopt-waiting">
                      {{ adoptPhase === 'exchanging' ? t('agentInstall.adoptExchanging') : adoptPhase === 'requesting' ? t('agentInstall.adoptRequesting') : t('agentInstall.adoptWaiting') }}
                    </p>
                    <template v-if="adoptPairingCode">
                      <p class="adopt-pairing-code" data-test="agent-adopt-pairing-code">
                        {{ t('agentInstall.adoptPairingCode', { code: adoptPairingCode }) }}
                      </p>
                      <p class="step-note" data-test="agent-adopt-pairing-hint">{{ t('agentInstall.adoptPairingCodeHint') }}</p>
                    </template>
                    <div class="step-actions step-actions-left">
                      <button class="settings-btn settings-btn-secondary" type="button" data-test="agent-adopt-cancel" @click="cancelAdoption">
                        {{ t('agentInstall.adoptCancel') }}
                      </button>
                    </div>
                  </template>
                </section>

                <section v-else class="install-phase" :class="`phase-${installStartStatus}`" data-test="install-phase-start">
                  <header class="install-phase-head">
                    <strong>{{ t('settings.agents.installPhaseStart') }}</strong>
                    <span class="phase-state">
                      {{ installStartMessage || t(installStartStatus === 'idle' ? 'settings.agents.installPhaseIdle' : installStartStatus === 'running' ? 'settings.agents.installStartRunning' : installStartStatus === 'success' ? 'settings.agents.installStarted' : 'common.requestFailed') }}
                    </span>
                  </header>

                  <template v-if="installMode === 'generated_command'">
                    <p class="step-note">{{ t('settings.agents.installPreviewHint') }}</p>
                    <p class="step-note">{{ t('settings.agents.generatedCommandPhaseHint') }}</p>
                    <div class="settings-field">
                      <label class="settings-field-label">{{ t('settings.agents.controllerURL') }}</label>
                      <input v-model="installForm.controllerURL" class="settings-input" data-test="agent-install-controller-url" />
                    </div>
                    <div class="settings-field">
                      <label class="settings-field-label">{{ t('settings.agents.releaseBaseURL') }}</label>
                      <input v-model="installForm.releaseBaseURL" class="settings-input" data-test="agent-install-release-base-url" />
                    </div>
                    <div class="settings-field small-field">
                      <label class="settings-field-label">{{ t('settings.agents.tokenTTL') }}</label>
                      <input v-model.number="installForm.tokenTTLMinutes" class="settings-input" type="number" min="1" />
                    </div>
                    <div class="step-actions step-actions-left">
                      <button class="settings-btn settings-btn-primary" type="button" :disabled="generatingInstall" data-test="agent-install-generate" @click="generateInstallCommand">
                        {{ generatingInstall ? t('common.loading') : t('settings.agents.generateCommand') }}
                      </button>
                    </div>
                    <pre v-if="installResult" class="command-block" data-test="agent-install-command">{{ installResult.command }}</pre>
                    <div v-if="installResult" class="step-actions step-actions-left">
                      <button class="settings-btn settings-btn-secondary" type="button" @click="copyCommand">{{ t('common.copy') }}</button>
                      <button class="settings-btn settings-btn-primary" type="button" :disabled="checkingGeneratedInstall" data-test="agent-install-command-executed" @click="confirmGeneratedInstallExecuted">
                        {{ checkingGeneratedInstall ? t('common.loading') : t('settings.agents.installCommandExecuted') }}
                      </button>
                    </div>
                  </template>

                  <template v-else>
                    <p class="install-note">{{ t('settings.agents.pushOverSSHNote') }}</p>
                    <p v-if="pushSSHBlocked" class="install-note warning" data-test="agent-install-push-blocker">{{ t('settings.agents.pushOverSSHBlocked') }}</p>
                    <button class="settings-btn settings-btn-primary" type="button" :disabled="installingPush || pushSSHBlocked" data-test="agent-install-push" @click="pushInstall()">
                      {{ installingPush ? t('common.loading') : t('settings.agents.installStartNow') }}
                    </button>
                    <p v-if="pushInstallResult" class="install-note" data-test="agent-install-push-result">{{ pushInstallResult.message }}</p>
                  </template>
                </section>

                <section class="install-phase" :class="`phase-${installSecurityStatus}`" data-test="install-phase-security">
                  <header class="install-phase-head">
                    <strong>{{ t('settings.agents.installPhaseSecurity') }}</strong>
                    <span class="phase-state">
                      {{ installSecurityMessage || t(installSecurityStatus === 'idle' ? 'settings.agents.installSecurityNotStarted' : installSecurityStatus === 'waiting' ? 'settings.agents.installSecurityWaitingForStart' : installSecurityStatus === 'running' ? 'settings.agents.installSecurityRunning' : installSecurityStatus === 'success' ? 'settings.agents.installConnected' : 'common.requestFailed') }}
                    </span>
                  </header>
                  <p class="step-note">{{ t('settings.agents.installSecurityHint') }}</p>
                  <div v-if="pendingManualRestart && installResult?.restart_command" class="manual-restart">
                    <p class="step-note">{{ t('settings.agents.installRestartCommandHint') }}</p>
                    <pre class="command-block" data-test="agent-restart-command">{{ installResult.restart_command }}</pre>
                    <div class="step-actions step-actions-left">
                      <button class="settings-btn settings-btn-secondary" type="button" @click="copyRestartCommand">{{ t('common.copy') }}</button>
                      <button class="settings-btn settings-btn-primary" type="button" :disabled="checkingRestart" data-test="agent-restart-command-executed" @click="confirmAgentRestarted">
                        {{ checkingRestart ? t('common.loading') : t('settings.agents.installRestartCommandExecuted') }}
                      </button>
                    </div>
                  </div>
                  <div v-if="canRetryInstallSecurityCheck" class="step-actions step-actions-left">
                    <button class="settings-btn settings-btn-secondary" type="button" data-test="agent-install-security-retry" @click="retryInstallSecurityCheck">
                      {{ t('settings.agents.installSecurityRetryCheck') }}
                    </button>
                  </div>
                </section>
              </div>
            </template>
          </section>

          <section v-if="activeTab === 'transport'" class="panel-step">
            <div class="chain-toolbar">
              <button type="button" class="settings-btn" data-test="transport-add-direct" @click="addEntry('direct')">direct</button>
              <button type="button" class="settings-btn" data-test="transport-add-tunnel" @click="addEntry('tunnel')">tunnel</button>
            </div>
            <div v-if="canProbe && transportDirty" class="settings-alert settings-alert-warning" data-test="agent-transport-dirty">
              {{ t('settings.agents.saveTransportBeforeProbe') }}
            </div>
            <div v-if="tunnelTargetDirty" class="settings-alert settings-alert-warning" data-test="agent-transport-tunnel-invalidation">
              {{ t('settings.agents.tunnelInvalidationWarning') }}
            </div>
            <div v-if="bindScopeDirty" class="settings-alert settings-alert-warning" data-test="agent-bind-scope-dirty">
              {{ t('settings.agents.bindScopeDirtyHint') }}
            </div>
            <section v-for="(entry, index) in chain" :key="`${entry.type}-${index}`" class="transport-entry" :data-test="`transport-entry-${index}`">
              <header class="transport-entry-head">
                <strong>{{ index + 1 }}. {{ entry.type }}</strong>
                <span v-if="index === 0" class="priority-badge">{{ t('settings.agents.primaryTransport') }}</span>
                <span v-else class="priority-badge degraded">{{ t('settings.agents.fallbackTransport') }}</span>
                <button type="button" class="icon-btn" :data-test="`transport-move-up-${index}`" @click="moveEntry(index, -1)">↑</button>
                <button type="button" class="icon-btn" :data-test="`transport-move-down-${index}`" @click="moveEntry(index, 1)">↓</button>
                <button type="button" class="icon-btn danger" :data-test="`transport-remove-${index}`" @click="removeEntry(index)">×</button>
              </header>

              <div v-if="entry.type === 'direct' && entry.direct" class="settings-field">
                <label class="settings-field-label">{{ t('settings.agents.directAddress') }}</label>
                <input v-model="entry.direct.address" class="settings-input" placeholder="agent.example.com:57017" :data-test="`direct-address-input-${index}`" />
                <div v-if="directOptions.length" class="direct-address-tags">
                  <button
                    v-for="option in directOptions"
                    :key="option.key"
                    type="button"
                    class="address-tag"
                    :data-test="`direct-address-option-${option.key}-${index}`"
                    @click="applyDirectAddressOption(index, option.address)"
                  >
                    {{ t(option.labelKey, { address: option.address }) }}
                  </button>
                </div>
              </div>

              <div v-if="entry.type === 'tunnel' && entry.tunnel" class="settings-field">
                <label class="settings-field-label">{{ t('settings.agents.tunnelLoopback') }}</label>
                <span class="field-hint" :data-test="`tunnel-loopback-note-${index}`">
                  {{ t('settings.agents.tunnelLoopbackHint', { port: bindPort }) }}
                </span>
              </div>

              <footer v-if="canProbe" class="transport-entry-actions">
                <button type="button" class="settings-btn" :disabled="needsCreateBeforeNextStep || transportDirty" :data-test="`transport-test-${index}`" @click="testEntry(index)">
                  {{ t('settings.agents.testTransport') }}
                </button>
                <span class="probe-result">{{ probeLabel(probeResults[index]) }}</span>
              </footer>
            </section>
            <footer class="step-actions">
              <button type="button" class="settings-btn settings-btn-primary" :disabled="savingTransport || creatingAgent || (needsCreateBeforeNextStep && !hostID)" data-test="agent-transport-save" @click="saveTransport">
                {{ savingTransport || creatingAgent ? t('common.loading') : t(needsCreateBeforeNextStep ? 'settings.agents.createAndContinue' : 'common.save') }}
              </button>
            </footer>
          </section>

          <section v-if="activeTab === 'probe'" class="panel-step">
            <div v-if="!canProbe" class="settings-alert settings-alert-warning" data-test="agent-probe-locked">
              <p>{{ t('settings.agents.probeLocked') }}</p>
              <button type="button" class="settings-btn settings-btn-primary" data-test="agent-probe-go-install" @click="selectTab('install')">
                {{ t('settings.agents.goInstall') }}
              </button>
            </div>
            <template v-else>
              <div v-if="transportDirty" class="settings-alert settings-alert-warning" data-test="agent-probe-dirty">
                {{ t('settings.agents.saveTransportBeforeProbe') }}
              </div>
              <button type="button" class="settings-btn settings-btn-primary" :disabled="probingAll || transportDirty" data-test="agent-probe-run" @click="probeAll">
                {{ probingAll ? t('common.loading') : t('settings.agents.runProbe') }}
              </button>
              <div class="route-detail-list">
                <div v-for="row in currentRows" :key="row.index" class="route-detail-row" :data-test="`agent-probe-result-${row.index}`">
                  <span class="route-index">{{ row.index + 1 }}</span>
                  <span class="route-address">{{ transportLabel(row.type) }} · {{ row.address }}</span>
                  <span class="route-status">{{ probeLabel(probeResults[row.index] ?? row.probe) }}</span>
                </div>
              </div>
            </template>
          </section>
        </main>
      </div>
    </section>
  </div>
</template>

<style scoped>
.agent-config-panel {
  width: min(920px, calc(100vw - 32px));
}
.panel-layout {
  display: grid;
  grid-template-columns: 180px minmax(0, 1fr);
  min-height: min(620px, calc(100vh - 160px));
}
.panel-tabs {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-secondary);
}
.panel-tab {
  display: flex;
  min-height: 36px;
  align-items: center;
  gap: 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  text-align: left;
}
.panel-tab:hover,
.panel-tab.active {
  border-color: var(--border);
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.panel-tab.locked {
  color: var(--text-tertiary);
}
.tab-state {
  width: 16px;
  color: var(--text-tertiary);
  font-size: 10px;
}
.panel-body {
  padding: 14px 16px;
}
.panel-step {
  display: grid;
  gap: 12px;
}
.row,
.segmented,
.security-status,
.step-actions,
.readonly-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.segmented label,
.security-status,
.field-hint,
.step-note,
.install-note {
  color: var(--text-secondary);
  font-size: 12px;
}
.flex {
  flex: 1;
}
.port,
.small-field {
  width: 132px;
}
.manual-tls {
  display: grid;
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  background: var(--bg-primary);
}
.cert-box {
  min-height: 120px;
  font-family: var(--font-mono, monospace);
  resize: vertical;
}
.adopt-pairing-code {
  margin: 0;
  color: var(--text-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.16em;
}
.force-reinstall-block {
  display: grid;
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  background: var(--bg-primary);
}
.force-reinstall-consent {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}
.step-actions {
  justify-content: flex-end;
}
.step-actions-left {
  justify-content: flex-start;
}
.install-phases {
  display: grid;
  gap: 12px;
}
.manual-restart {
  display: grid;
  gap: 8px;
}
.install-phase {
  display: grid;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  background: var(--bg-primary);
}
.install-phase-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.phase-state {
  color: var(--text-secondary);
  font-size: 12px;
}
.phase-running,
.phase-waiting {
  border-color: rgba(245, 158, 11, 0.45);
}
.phase-running .phase-state,
.phase-waiting .phase-state {
  color: #f59e0b;
}
.phase-success {
  border-color: rgba(34, 197, 94, 0.45);
}
.phase-success .phase-state {
  color: #22c55e;
}
.phase-error {
  border-color: rgba(239, 68, 68, 0.45);
}
.phase-error .phase-state {
  color: #ef4444;
}
.direct-address-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.address-tag {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  line-height: 1.2;
  padding: 4px 8px;
}
.address-tag:hover {
  border-color: var(--border);
  color: var(--text-primary);
}
.command-block {
  max-height: 180px;
  overflow: auto;
  padding: 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  white-space: pre-wrap;
  word-break: break-all;
}
.route-detail-list {
  display: grid;
  gap: 8px;
}
.route-detail-row {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr) minmax(150px, auto);
  gap: 8px;
  align-items: center;
  padding: 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  font-size: 12px;
}
.route-index,
.route-address {
  font-family: var(--font-mono, monospace);
}
.route-address {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.route-status {
  color: var(--text-tertiary);
}
@media (max-width: 760px) {
  .panel-layout {
    grid-template-columns: 1fr;
  }
  .panel-tabs {
    flex-direction: row;
    overflow-x: auto;
    border-right: 0;
    border-bottom: 1px solid var(--border-secondary);
  }
  .panel-tab {
    flex: 0 0 auto;
  }
}
</style>
