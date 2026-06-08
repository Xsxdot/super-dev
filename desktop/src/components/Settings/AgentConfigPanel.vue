<!--
AgentConfigPanel：统一管理单台 Host Agent 的监听、安全、安装、连接链和探测。

职责：
  - 将原先分散的安全配置、安装命令、连接链编辑收敛到一个四步面板
  - 保证监听地址、监听端口、TLS 模式只有一个编辑入口
  - 通过 agents store 调用现有 Agent API 完成保存、下发、生成命令和探测

边界：
  - 不创建或编辑 Host 身份信息
  - 不直接调用 fetch 或底层 HTTP API
  - 不修改后端 DTO 结构
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentsStore } from '@/stores/agents'
import { agentRouteRows } from '@/lib/agentRoute'
import { runtimeFor, type AgentPanelTab } from '@/lib/agentStage'
import type {
  AgentConfigUpdatePayload,
  AgentCreatePayload,
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
const probingAll = ref(false)
const manualAdvancedOpen = ref(false)
const installMode = ref<'generated_command' | 'push_over_ssh'>('generated_command')
const installResult = ref<AgentInstallCommandResponse | null>(null)
const chain = ref<TransportEntry[]>([])
const savedChain = ref<TransportEntry[]>([])
const probeResults = reactive<Record<number, ProbeResult | null>>({})
const hostID = ref('')
const localCreatedAgent = ref<AgentDTO | null>(null)

const securityForm = reactive({
  listenAddress: '',
  listenPort: 57017,
  tlsMode: 'auto' as AgentTLSMode,
  serverName: '',
  caCert: '',
})

const installForm = reactive({
  controllerURL: 'http://127.0.0.1:57017',
  tokenTTLMinutes: 30,
})

const isCreateMode = computed(() => props.mode === 'create')
const selectedHost = computed(() => props.hosts?.find(host => host.id === hostID.value))
const persistedAgent = computed<AgentDTO | null>(() => props.agent ?? localCreatedAgent.value)
const panelAgent = computed<AgentDTO | null>(() => {
  if (persistedAgent.value) return persistedAgent.value
  if (!isCreateMode.value || !selectedHost.value) return null
  return {
    host_id: selectedHost.value.id,
    host_name: selectedHost.value.name,
    tags: selectedHost.value.tags,
    transport: { chain: chain.value.map(normalizeEntry) },
    config: { listen_address: securityForm.listenAddress, listen_port: bindPort.value },
    runtime: { installed: false, health: 'unknown', reachable: false },
    security: { token_configured: false, provision_state: 'pending-bootstrap', tls: { mode: securityForm.tlsMode } },
  }
})
const title = computed(() => isCreateMode.value && !persistedAgent.value
  ? t('settings.agents.createTitle')
  : t('settings.agents.panelTitle', { name: panelAgent.value?.host_name ?? '' }))
const runtime = computed(() => persistedAgent.value ? runtimeFor(persistedAgent.value, props.node) : undefined)
const canProbe = computed(() => runtime.value?.installed === true)
const bindAddress = computed(() => securityForm.listenAddress.trim() || '127.0.0.1')
const bindPort = computed(() => Number(securityForm.listenPort) || 57017)
const installTransportType = computed(() => chain.value[0]?.type ?? 'tunnel')
const transportDirty = computed(() => chainSignature(chain.value) !== chainSignature(savedChain.value))
const currentRows = computed(() => panelAgent.value
  ? agentRouteRows({ ...panelAgent.value, transport: { chain: chain.value } }, transportDirty.value ? undefined : props.node)
  : [])
const needsCreateBeforeNextStep = computed(() => isCreateMode.value && !persistedAgent.value)

const tabs = computed<Array<{ key: AgentPanelTab; label: string; locked: boolean; done: boolean }>>(() => [
  { key: 'security', label: t('settings.agents.tabSecurity'), locked: false, done: Boolean(persistedAgent.value?.config?.listen_port) },
  { key: 'transport', label: t('settings.agents.tabTransport'), locked: false, done: Boolean(persistedAgent.value?.transport?.chain?.length) },
  { key: 'install', label: t('settings.agents.tabInstall'), locked: needsCreateBeforeNextStep.value, done: runtime.value?.installed === true },
  { key: 'probe', label: t('settings.agents.tabProbe'), locked: !canProbe.value, done: runtime.value?.health === 'healthy' },
])

function defaultEntry(type: TransportType): TransportEntry {
  if (type === 'tunnel') return { type, tunnel: { remote_agent_port: 57017 } }
  if (type === 'direct') return { type, direct: { address: '' } }
  return { type }
}

function normalizeEntry(entry: TransportEntry): TransportEntry {
  if (entry.type === 'tunnel') {
    return { type: 'tunnel', tunnel: { remote_agent_port: Number(entry.tunnel?.remote_agent_port) || 57017 } }
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

function clearProbeResults() {
  Object.keys(probeResults).forEach(key => delete probeResults[Number(key)])
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
  localCreatedAgent.value = null
  activeTab.value = props.initialTab ?? 'security'
  actionError.value = null
  manualAdvancedOpen.value = false
  installMode.value = 'generated_command'
  installResult.value = null
  hostID.value = agent?.host_id ?? props.hosts?.[0]?.id ?? ''
  securityForm.listenAddress = agent?.config?.listen_address ?? ''
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
      listen_address: securityForm.listenAddress.trim(),
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
      listen_address: securityForm.listenAddress.trim(),
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
    await agentsStore.updateAgentConfig(agent.host_id, securityPayload())
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
  try {
    installResult.value = await agentsStore.generateInstallCommand(agent.host_id, {
      method: 'generated_command',
      controller_url: installForm.controllerURL.trim(),
      bind_address: bindAddress.value,
      remote_agent_port: bindPort.value,
      transport_type: installTransportType.value,
      token_ttl_minutes: Number(installForm.tokenTTLMinutes) || 30,
    })
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

function addEntry(type: TransportType) {
  chain.value = [...chain.value, defaultEntry(type)]
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
      localCreatedAgent.value = created
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
    await agentsStore.updateAgentTransport(agent.host_id, payload)
    chain.value = normalizedChain.map(normalizeEntry)
    savedChain.value = normalizedChain.map(normalizeEntry)
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    savingTransport.value = false
  }
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

watch(
  () => [props.visible, props.agent, props.initialTab] as const,
  ([visible, agent]) => {
    if (visible) reset(agent)
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
</script>

<template>
  <div v-if="visible && (persistedAgent || isCreateMode)" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <section class="settings-modal settings-modal-wide agent-config-panel" data-test="agent-panel">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ title }}</h2>
        <button type="button" class="settings-btn settings-btn-ghost" @click="emit('cancel')">{{ t('common.close') }}</button>
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
              <div class="settings-field flex">
                <label class="settings-field-label">{{ t('settings.agents.bindAddress') }}</label>
                <input v-model="securityForm.listenAddress" class="settings-input" data-test="agent-listen-address" />
              </div>
              <div class="settings-field port">
                <label class="settings-field-label">{{ t('settings.agents.remotePort') }}</label>
                <input v-model.number="securityForm.listenPort" class="settings-input" type="number" min="1" data-test="agent-listen-port" />
              </div>
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

            <template v-if="installMode === 'generated_command'">
              <div class="readonly-chips">
                <span class="settings-badge" data-test="agent-install-bind-preview">{{ t('settings.agents.installBindPreview', { address: bindAddress, port: bindPort }) }}</span>
                <span class="settings-badge">{{ t('settings.agents.installTLSPreview', { mode: securityForm.tlsMode }) }}</span>
                <span class="settings-badge">{{ t('settings.agents.installTransportPreview', { type: installTransportType }) }}</span>
              </div>
              <p class="step-note">{{ t('settings.agents.installPreviewHint') }}</p>
              <div class="settings-field">
                <label class="settings-field-label">{{ t('settings.agents.controllerURL') }}</label>
                <input v-model="installForm.controllerURL" class="settings-input" data-test="agent-install-controller-url" />
              </div>
              <div class="settings-field small-field">
                <label class="settings-field-label">{{ t('settings.agents.tokenTTL') }}</label>
                <input v-model.number="installForm.tokenTTLMinutes" class="settings-input" type="number" min="1" />
              </div>
              <button class="settings-btn settings-btn-primary" type="button" :disabled="generatingInstall" data-test="agent-install-generate" @click="generateInstallCommand">
                {{ generatingInstall ? t('common.loading') : t('settings.agents.generateCommand') }}
              </button>
              <pre v-if="installResult" class="command-block" data-test="agent-install-command">{{ installResult.command }}</pre>
              <button v-if="installResult" class="settings-btn settings-btn-secondary" type="button" @click="copyCommand">{{ t('common.copy') }}</button>
            </template>

            <p v-else class="install-note">{{ t('settings.agents.pushOverSSHNote') }}</p>
            </template>
          </section>

          <section v-if="activeTab === 'transport'" class="panel-step">
            <div class="chain-toolbar">
              <button type="button" class="settings-btn" data-test="transport-add-direct" @click="addEntry('direct')">direct</button>
              <button type="button" class="settings-btn" data-test="transport-add-tunnel" @click="addEntry('tunnel')">tunnel</button>
            </div>
            <div v-if="transportDirty" class="settings-alert settings-alert-warning" data-test="agent-transport-dirty">
              {{ t('settings.agents.saveTransportBeforeProbe') }}
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
                <input v-model="entry.direct.address" class="settings-input" placeholder="100.64.0.8:57017" />
              </div>

              <div v-if="entry.type === 'tunnel' && entry.tunnel" class="settings-field">
                <label class="settings-field-label">{{ t('settings.hostForm.remoteAgentPort') }}</label>
                <input v-model.number="entry.tunnel.remote_agent_port" class="settings-input" type="number" min="1" :data-test="`tunnel-remote-agent-port-${index}`" />
              </div>

              <footer class="transport-entry-actions">
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
                  <span class="route-address">{{ row.type }} · {{ row.address }}</span>
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
.step-actions {
  justify-content: flex-end;
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
