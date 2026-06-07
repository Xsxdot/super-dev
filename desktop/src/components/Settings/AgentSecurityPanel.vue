<!--
AgentSecurityPanel：编辑 Agent 统一运行与安全配置。

职责：
  - 收集 Agent listen 配置、TLS 模式、CA 和 server name
  - 通过 Agent config API 保存统一配置
  - 以 Agent 级入口执行安全下发

边界：
  - 不编辑 transport chain
  - 不编辑 Host SSH 登录信息
  - 不生成安装命令
-->
<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentsStore } from '@/stores/agents'
import type { AgentConfigUpdatePayload, AgentDTO, AgentTLSMode } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  agent?: AgentDTO | null
}>()

const emit = defineEmits<{
  cancel: []
}>()

const { t } = useAppI18n()
const agentsStore = useAgentsStore()
const saving = ref(false)
const provisioning = ref(false)
const actionError = ref<string | null>(null)
const form = reactive({
  listenAddress: '',
  listenPort: 57017,
  tlsMode: 'auto' as AgentTLSMode,
  serverName: '',
  caCert: '',
})

function reset(agent?: AgentDTO | null) {
  form.listenAddress = agent?.config?.listen_address ?? ''
  form.listenPort = agent?.config?.listen_port || 57017
  const mode = agent?.security?.tls?.mode
  form.tlsMode = mode === 'off' || mode === 'manual' ? mode : 'auto'
  form.serverName = agent?.security?.tls?.server_name ?? ''
  form.caCert = agent?.security?.tls?.ca_cert ?? ''
  actionError.value = null
}

function payload(): AgentConfigUpdatePayload {
  const tls: AgentConfigUpdatePayload['security']['tls'] = { mode: form.tlsMode }
  if (form.serverName.trim()) {
    tls.server_name = form.serverName.trim()
  }
  if (form.tlsMode === 'manual' && form.caCert.trim()) {
    tls.ca_cert = form.caCert
  }
  return {
    config: {
      listen_address: form.listenAddress.trim(),
      listen_port: Number(form.listenPort) || 57017,
    },
    security: {
      token_configured: Boolean(props.agent?.security?.token_configured),
      provision_state: props.agent?.security?.provision_state || 'pending-bootstrap',
      tls,
    },
  }
}

async function save() {
  if (!props.agent) return
  saving.value = true
  actionError.value = null
  try {
    await agentsStore.updateAgentConfig(props.agent.host_id, payload())
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    saving.value = false
  }
}

async function provision() {
  if (!props.agent) return
  provisioning.value = true
  actionError.value = null
  try {
    await agentsStore.provisionAgent(props.agent.host_id, { index: 0, tls_mode: form.tlsMode })
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    provisioning.value = false
  }
}

watch(
  () => [props.visible, props.agent] as const,
  ([visible, agent]) => {
    if (visible) reset(agent)
  },
  { immediate: true },
)
</script>

<template>
  <div v-if="visible && agent" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <section class="settings-modal agent-security-modal">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ t('settings.agents.securityConfig') }}</h2>
      </header>

      <div class="settings-modal-body security-body">
        <div v-if="actionError" class="settings-alert settings-alert-danger">{{ actionError }}</div>

        <div class="row">
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.agents.bindAddress') }}</label>
            <input v-model="form.listenAddress" class="settings-input" data-test="agent-listen-address" />
          </div>
          <div class="settings-field port">
            <label class="settings-field-label">{{ t('settings.agents.remotePort') }}</label>
            <input v-model.number="form.listenPort" class="settings-input" type="number" min="1" data-test="agent-listen-port" />
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.agents.tlsMode') }}</label>
          <div class="segmented">
            <label><input v-model="form.tlsMode" type="radio" value="off" data-test="agent-tls-mode-off" /> {{ t('settings.agents.tlsOff') }}</label>
            <label><input v-model="form.tlsMode" type="radio" value="auto" data-test="agent-tls-mode-auto" /> {{ t('settings.agents.tlsAuto') }}</label>
            <label><input v-model="form.tlsMode" type="radio" value="manual" data-test="agent-tls-mode-manual" /> {{ t('settings.agents.tlsManual') }}</label>
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.agents.serverName') }}</label>
          <input v-model="form.serverName" class="settings-input" data-test="agent-server-name" />
        </div>

        <div v-if="form.tlsMode === 'manual'" class="settings-field">
          <label class="settings-field-label">{{ t('settings.agents.caCert') }}</label>
          <textarea v-model="form.caCert" class="settings-input cert-box" data-test="agent-ca-cert" />
        </div>

        <div class="security-status">
          <span>{{ t('settings.agents.provisionState') }}: {{ agent.security.provision_state }}</span>
          <span>{{ t('settings.agents.tokenConfigured') }}: {{ agent.security.token_configured ? t('common.yes') : t('common.no') }}</span>
        </div>
      </div>

      <footer class="settings-modal-footer">
        <button type="button" class="settings-btn" @click="emit('cancel')">{{ t('common.close') }}</button>
        <button type="button" class="settings-btn settings-btn-secondary" :disabled="provisioning" data-test="agent-provision-security" @click="provision">
          {{ provisioning ? t('common.loading') : t('settings.agents.provisionSecurity') }}
        </button>
        <button type="button" class="settings-btn settings-btn-primary" :disabled="saving" data-test="agent-security-save" @click="save">
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.security-body {
  display: grid;
  gap: 10px;
}
.row,
.segmented,
.security-status {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.segmented label,
.security-status {
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
