<!--
AgentConfigModal：编辑 Host Agent 的连接方式。

职责：
  - 收集 tunnel/direct transport 参数
  - 生成 AgentUpdatePayload 交由父组件保存
  - 展示 mq/bridge 预留但不可选的连接类型

边界：
  - 不保存 Host 身份字段
  - 不执行安装命令生成
  - 不直接调用 Agent API
-->
<script setup lang="ts">
import { reactive, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { AgentDTO, AgentUpdatePayload, TransportType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  agent?: AgentDTO | null
}>()

const emit = defineEmits<{
  submit: [payload: AgentUpdatePayload]
  cancel: []
}>()

const { t } = useAppI18n()
const form = reactive({
  type: 'direct' as TransportType,
  directAddress: '',
  directTLS: false,
  sshHost: '',
  sshPort: 22,
  sshUser: 'root',
  sshPassword: '',
  sshKeyPath: '',
  sshPrivateKey: '',
  remoteAgentPort: 57017,
})

watch(
  () => [props.visible, props.agent] as const,
  ([visible, agent]) => {
    if (!visible) return
    const transport = agent?.transport
    form.type = transport?.type === 'tunnel' ? 'tunnel' : transport?.type === 'direct' ? 'direct' : 'direct'
    form.directAddress = transport?.direct?.address ?? ''
    form.directTLS = transport?.direct?.tls ?? false
    form.sshHost = transport?.tunnel?.ssh_host ?? ''
    form.sshPort = transport?.tunnel?.ssh_port ?? 22
    form.sshUser = transport?.tunnel?.ssh_user ?? 'root'
    form.sshPassword = transport?.tunnel?.ssh_password ?? ''
    form.sshKeyPath = transport?.tunnel?.ssh_key_path ?? ''
    form.sshPrivateKey = transport?.tunnel?.ssh_private_key ?? ''
    form.remoteAgentPort = transport?.tunnel?.remote_agent_port ?? 57017
  },
  { immediate: true },
)

function submit() {
  if (form.type === 'tunnel') {
    emit('submit', {
      transport: {
        type: 'tunnel',
        tunnel: {
          ssh_host: form.sshHost,
          ssh_port: Number(form.sshPort) || 22,
          ssh_user: form.sshUser,
          ssh_password: form.sshPassword,
          ssh_key_path: form.sshKeyPath,
          ssh_private_key: form.sshPrivateKey,
          remote_agent_port: Number(form.remoteAgentPort) || 57017,
        },
      },
    })
    return
  }
  emit('submit', {
    transport: {
      type: 'direct',
      direct: {
        address: form.directAddress,
        tls: form.directTLS,
      },
    },
  })
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <section class="settings-modal agent-config-modal">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ t('settings.agents.editConnection') }}</h2>
      </header>
      <div class="settings-modal-body agent-config-body">
        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.agents.transport') }}</label>
          <div class="segmented">
            <label><input v-model="form.type" type="radio" value="direct" /> direct</label>
            <label><input v-model="form.type" type="radio" value="tunnel" /> tunnel</label>
            <label class="disabled"><input type="radio" disabled /> mq</label>
            <label class="disabled"><input type="radio" disabled /> bridge</label>
          </div>
        </div>

        <template v-if="form.type === 'direct'">
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.agents.directAddress') }}</label>
            <input v-model="form.directAddress" class="settings-input" placeholder="100.64.0.8:57017" data-test="agent-direct-address" />
          </div>
          <label class="inline-check">
            <input v-model="form.directTLS" type="checkbox" data-test="agent-direct-tls" />
            {{ t('settings.agents.useTLS') }}
          </label>
        </template>

        <template v-else>
          <div class="row">
            <div class="settings-field flex">
              <label class="settings-field-label">{{ t('settings.hostForm.sshAddress') }}</label>
              <input v-model="form.sshHost" class="settings-input" data-test="agent-tunnel-host" />
            </div>
            <div class="settings-field port">
              <label class="settings-field-label">{{ t('settings.hostForm.port') }}</label>
              <input v-model.number="form.sshPort" class="settings-input" type="number" min="1" />
            </div>
          </div>
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.hostForm.sshUser') }}</label>
            <input v-model="form.sshUser" class="settings-input" />
          </div>
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.hostForm.sshPassword') }}</label>
            <input v-model="form.sshPassword" class="settings-input" type="password" />
          </div>
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.hostForm.sshKeyPath') }}</label>
            <input v-model="form.sshKeyPath" class="settings-input" placeholder="~/.ssh/id_ed25519" />
          </div>
          <div class="settings-field">
            <label class="settings-field-label">{{ t('settings.hostForm.remoteAgentPort') }}</label>
            <input v-model.number="form.remoteAgentPort" class="settings-input" type="number" min="1" />
          </div>
        </template>
      </div>
      <footer class="settings-modal-footer">
        <button type="button" class="settings-btn" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button type="button" class="settings-btn settings-btn-primary" data-test="agent-config-submit" @click="submit">{{ t('common.save') }}</button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.agent-config-body {
  display: grid;
  gap: 10px;
}
.segmented {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.segmented label,
.inline-check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-secondary);
  font-size: 12px;
}
.segmented .disabled {
  color: var(--text-tertiary);
}
.row {
  display: flex;
  gap: 8px;
}
.flex { flex: 1; }
.port { width: 90px; }
</style>
