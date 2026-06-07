<!--
AgentCreateModal：新增 Agent 的分步配置表单。

职责：
  - 选择已有 Host 作为 Agent 归属
  - 收集 transport chain 中各连接方式自己的配置
  - 收集 Agent 统一 listen/TLS 安全配置

边界：
  - 不编辑 Host SSH 登录信息
  - 不执行安装命令生成或安全下发
  - 不直接调用 HTTP API，提交 payload 给父组件
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { AgentCreatePayload, AgentTLSMode, Host, TransportEntry, TransportType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  hosts: Host[]
}>()

const emit = defineEmits<{
  submit: [payload: AgentCreatePayload]
  cancel: []
}>()

const { t } = useAppI18n()
const step = ref(1)
const hostID = ref('')
const chain = ref<TransportEntry[]>([])
const listenAddress = ref('')
const listenPort = ref(57017)
const tlsMode = ref<AgentTLSMode>('auto')
const serverName = ref('')
const caCert = ref('')

const canGoNext = computed(() => {
  if (step.value === 1) return Boolean(hostID.value)
  if (step.value === 2) return chain.value.length > 0
  return true
})

function defaultEntry(type: TransportType): TransportEntry {
  if (type === 'tunnel') return { type, tunnel: { remote_agent_port: 57017 } }
  return { type: 'direct', direct: { address: '' } }
}

function normalizeEntry(entry: TransportEntry): TransportEntry {
  if (entry.type === 'tunnel') {
    return { type: 'tunnel', tunnel: { remote_agent_port: entry.tunnel?.remote_agent_port || 57017 } }
  }
  if (entry.type === 'direct') {
    return { type: 'direct', direct: { address: entry.direct?.address ?? '' } }
  }
  return { type: entry.type }
}

function addEntry(type: TransportType) {
  chain.value = [...chain.value, defaultEntry(type)]
}

function removeEntry(index: number) {
  if (chain.value.length <= 1) return
  chain.value = chain.value.filter((_, i) => i !== index)
}

function next() {
  if (step.value < 3 && canGoNext.value) step.value += 1
}

function previous() {
  if (step.value > 1) step.value -= 1
}

function submit() {
  const tls: AgentCreatePayload['security']['tls'] = { mode: tlsMode.value }
  if (serverName.value.trim()) tls.server_name = serverName.value.trim()
  if (tlsMode.value === 'manual' && caCert.value.trim()) tls.ca_cert = caCert.value
  emit('submit', {
    host_id: hostID.value,
    transport: { chain: chain.value.map(normalizeEntry) },
    config: {
      listen_address: listenAddress.value.trim(),
      listen_port: Number(listenPort.value) || 57017,
    },
    security: {
      token_configured: false,
      provision_state: 'pending-bootstrap',
      tls,
    },
  })
}

watch(
  () => props.visible,
  visible => {
    if (!visible) return
    step.value = 1
    hostID.value = props.hosts[0]?.id ?? ''
    chain.value = [defaultEntry('direct')]
    listenAddress.value = ''
    listenPort.value = 57017
    tlsMode.value = 'auto'
    serverName.value = ''
    caCert.value = ''
  },
  { immediate: true },
)
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <section class="settings-modal agent-create-modal">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ t('settings.agents.createTitle') }}</h2>
      </header>

      <div class="settings-modal-body create-body">
        <template v-if="step === 1">
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.agents.createHost') }}</label>
            <select v-model="hostID" class="settings-select" data-test="agent-create-host">
              <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
            </select>
          </div>
        </template>

        <template v-if="step === 2">
          <div class="chain-toolbar">
            <button type="button" class="settings-btn" data-test="agent-create-add-direct" @click="addEntry('direct')">direct</button>
            <button type="button" class="settings-btn" data-test="agent-create-add-tunnel" @click="addEntry('tunnel')">tunnel</button>
          </div>
          <section
            v-for="(entry, index) in chain"
            :key="`${entry.type}-${index}`"
            class="transport-entry"
            :data-test="`agent-create-entry-${index}`"
          >
            <header class="transport-entry-head">
              <strong>{{ index + 1 }}. {{ entry.type }}</strong>
              <button type="button" class="icon-btn danger" :data-test="`agent-create-remove-${index}`" @click="removeEntry(index)">×</button>
            </header>
            <div v-if="entry.type === 'direct' && entry.direct" class="settings-field">
              <label class="settings-field-label">{{ t('settings.agents.directAddress') }}</label>
              <input v-model="entry.direct.address" class="settings-input" data-test="agent-create-direct-address" />
            </div>
            <div v-if="entry.type === 'tunnel' && entry.tunnel" class="settings-field">
              <label class="settings-field-label">{{ t('settings.hostForm.remoteAgentPort') }}</label>
              <input v-model.number="entry.tunnel.remote_agent_port" class="settings-input" type="number" min="1" data-test="agent-create-remote-port" />
            </div>
          </section>
        </template>

        <template v-if="step === 3">
          <div class="row">
            <div class="settings-field flex">
              <label class="settings-field-label">{{ t('settings.agents.bindAddress') }}</label>
              <input v-model="listenAddress" class="settings-input" data-test="agent-create-listen-address" />
            </div>
            <div class="settings-field port">
              <label class="settings-field-label">{{ t('settings.agents.remotePort') }}</label>
              <input v-model.number="listenPort" class="settings-input" type="number" min="1" data-test="agent-create-listen-port" />
            </div>
          </div>
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.agents.tlsMode') }}</label>
            <div class="segmented">
              <label><input v-model="tlsMode" type="radio" value="off" data-test="agent-create-tls-off" /> {{ t('settings.agents.tlsOff') }}</label>
              <label><input v-model="tlsMode" type="radio" value="auto" data-test="agent-create-tls-auto" /> {{ t('settings.agents.tlsAuto') }}</label>
              <label><input v-model="tlsMode" type="radio" value="manual" data-test="agent-create-tls-manual" /> {{ t('settings.agents.tlsManual') }}</label>
            </div>
          </div>
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.agents.serverName') }}</label>
            <input v-model="serverName" class="settings-input" data-test="agent-create-server-name" />
          </div>
          <div v-if="tlsMode === 'manual'" class="settings-field">
            <label class="settings-field-label">{{ t('settings.agents.caCert') }}</label>
            <textarea v-model="caCert" class="settings-input cert-box" data-test="agent-create-ca-cert" />
          </div>
        </template>
      </div>

      <footer class="settings-modal-footer">
        <button type="button" class="settings-btn" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button v-if="step > 1" type="button" class="settings-btn" data-test="agent-create-previous" @click="previous">{{ t('settings.agents.previous') }}</button>
        <button v-if="step < 3" type="button" class="settings-btn settings-btn-primary" :disabled="!canGoNext" data-test="agent-create-next" @click="next">{{ t('settings.agents.next') }}</button>
        <button v-else type="button" class="settings-btn settings-btn-primary" data-test="agent-create-submit" @click="submit">{{ t('common.save') }}</button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.create-body {
  display: grid;
  gap: 10px;
}
.row,
.segmented,
.chain-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.segmented label {
  color: var(--text-secondary);
  font-size: 12px;
}
.flex { flex: 1; }
.port { width: 116px; }
.cert-box {
  min-height: 120px;
  font-family: var(--font-mono, monospace);
  resize: vertical;
}
</style>
