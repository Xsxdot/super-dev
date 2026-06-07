<!--
AgentInstallModal：生成或展示 Agent 安装方式。

职责：
  - 为 selected Host 生成 curl | bash 安装命令
  - 收集安装命令所需 controller、绑定地址、端口和 token TTL
  - 提供命令复制入口

边界：
  - 不执行远端安装动作
  - 不写入 Host 或 Agent 配置
  - 不保存生成出的 token 或命令
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentsStore } from '@/stores/agents'
import type { AgentDTO, AgentInstallCommandResponse, TransportType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  agent?: AgentDTO | null
}>()

const emit = defineEmits<{
  cancel: []
}>()

const { t } = useAppI18n()
const agentsStore = useAgentsStore()
const mode = ref<'generated_command' | 'push_over_ssh'>('generated_command')
const loading = ref(false)
const error = ref<string | null>(null)
const result = ref<AgentInstallCommandResponse | null>(null)
const form = reactive({
  controllerURL: 'http://127.0.0.1:57017',
  bindAddress: '127.0.0.1',
  remoteAgentPort: 57017,
  transportType: 'tunnel' as TransportType,
  tokenTTLMinutes: 30,
})

const title = computed(() => t('settings.agents.installTitle', { name: props.agent?.host_name ?? '' }))

watch(
  () => props.visible,
  visible => {
    if (!visible) return
    mode.value = 'generated_command'
    error.value = null
    result.value = null
    form.transportType = props.agent?.transport.chain[0]?.type ?? 'tunnel'
  },
)

async function generate() {
  if (!props.agent) return
  loading.value = true
  error.value = null
  try {
    result.value = await agentsStore.generateInstallCommand(props.agent.host_id, {
      method: 'generated_command',
      controller_url: form.controllerURL,
      bind_address: form.bindAddress,
      remote_agent_port: Number(form.remoteAgentPort) || 57017,
      transport_type: form.transportType,
      token_ttl_minutes: Number(form.tokenTTLMinutes) || 30,
    })
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('common.requestFailed')
  } finally {
    loading.value = false
  }
}

async function copyCommand() {
  if (!result.value?.command) return
  await navigator.clipboard?.writeText(result.value.command)
}
</script>

<template>
  <div v-if="visible && agent" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <section class="settings-modal agent-install-modal">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ title }}</h2>
      </header>
      <div class="settings-modal-body install-body">
        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.agents.installMethod') }}</label>
          <div class="segmented">
            <label><input v-model="mode" type="radio" value="generated_command" /> {{ t('settings.agents.generatedCommand') }}</label>
            <label><input v-model="mode" type="radio" value="push_over_ssh" /> {{ t('settings.agents.pushOverSSH') }}</label>
          </div>
        </div>

        <template v-if="mode === 'generated_command'">
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.agents.controllerURL') }}</label>
            <input v-model="form.controllerURL" class="settings-input" data-test="agent-install-controller-url" />
          </div>
          <div class="row">
            <div class="settings-field flex">
              <label class="settings-field-label">{{ t('settings.agents.bindAddress') }}</label>
              <input v-model="form.bindAddress" class="settings-input" />
            </div>
            <div class="settings-field port">
              <label class="settings-field-label">{{ t('settings.agents.remotePort') }}</label>
              <input v-model.number="form.remoteAgentPort" class="settings-input" type="number" min="1" />
            </div>
          </div>
          <div class="row">
            <div class="settings-field flex">
              <label class="settings-field-label">{{ t('settings.agents.transport') }}</label>
              <select v-model="form.transportType" class="settings-select">
                <option value="tunnel">tunnel</option>
                <option value="direct">direct</option>
                <option value="mq">mq</option>
                <option value="bridge">bridge</option>
              </select>
            </div>
            <div class="settings-field port">
              <label class="settings-field-label">{{ t('settings.agents.tokenTTL') }}</label>
              <input v-model.number="form.tokenTTLMinutes" class="settings-input" type="number" min="1" />
            </div>
          </div>
          <button class="settings-btn settings-btn-primary" type="button" :disabled="loading" data-test="agent-install-generate" @click="generate">
            {{ loading ? t('common.loading') : t('settings.agents.generateCommand') }}
          </button>
          <div v-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>
          <pre v-if="result" class="command-block" data-test="agent-install-command">{{ result.command }}</pre>
          <button v-if="result" class="settings-btn settings-btn-secondary" type="button" @click="copyCommand">{{ t('common.copy') }}</button>
        </template>

        <p v-else class="install-note">{{ t('settings.agents.pushOverSSHNote') }}</p>
      </div>
      <footer class="settings-modal-footer">
        <button type="button" class="settings-btn" @click="emit('cancel')">{{ t('common.close') }}</button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.install-body {
  display: grid;
  gap: 10px;
}
.segmented,
.row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.segmented label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-secondary);
  font-size: 12px;
}
.flex { flex: 1; }
.port { width: 116px; }
.command-block {
  max-height: 160px;
  overflow: auto;
  padding: 10px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  font-size: 11px;
  white-space: pre-wrap;
  word-break: break-all;
}
.install-note {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
