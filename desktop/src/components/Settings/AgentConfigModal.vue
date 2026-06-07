<!--
AgentConfigModal：编辑 Host Agent 的有序连接链。

职责：
  - 收集 direct/tunnel transport chain 参数
  - 提供链路增删、排序、探测和安全下发入口
  - 生成 AgentUpdatePayload 交由父组件保存

边界：
  - 不保存 Host 身份字段
  - 不执行安装命令生成
  - 不直接调用底层 HTTP API，通过 agents store 处理动作
-->
<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentsStore } from '@/stores/agents'
import type { AgentDTO, AgentUpdatePayload, ProbeResult, TransportEntry, TransportType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  agent?: AgentDTO | null
}>()

const emit = defineEmits<{
  submit: [payload: AgentUpdatePayload]
  cancel: []
}>()

const { t } = useAppI18n()
const agentsStore = useAgentsStore()
const chain = ref<TransportEntry[]>([])
const probeResults = reactive<Record<number, ProbeResult | null>>({})
const actionError = ref<string | null>(null)

function defaultEntry(type: TransportType): TransportEntry {
  if (type === 'tunnel') {
    return { type, tunnel: { ssh_host: '', ssh_port: 22, ssh_user: 'root', remote_agent_port: 57017 } }
  }
  return { type: 'direct', direct: { address: '', tls: true, ca_cert: '' } }
}

function cloneChain(agent?: AgentDTO | null): TransportEntry[] {
  const source = agent?.transport?.chain?.length ? agent.transport.chain : [defaultEntry('direct')]
  return source.map(entry => ({
    type: entry.type,
    direct: entry.direct ? { ...entry.direct } : undefined,
    tunnel: entry.tunnel ? { ...entry.tunnel } : undefined,
  }))
}

function addEntry(type: TransportType) {
  chain.value = [...chain.value, defaultEntry(type)]
}

function removeEntry(index: number) {
  if (chain.value.length <= 1) return
  chain.value = chain.value.filter((_, i) => i !== index)
}

function moveEntry(index: number, direction: -1 | 1) {
  const next = [...chain.value]
  const target = index + direction
  if (target < 0 || target >= next.length) return
  const [item] = next.splice(index, 1)
  next.splice(target, 0, item)
  chain.value = next
}

async function testEntry(index: number) {
  if (!props.agent) return
  actionError.value = null
  try {
    probeResults[index] = await agentsStore.testTransport(props.agent.host_id, index)
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  }
}

async function provisionEntry(index: number) {
  if (!props.agent) return
  actionError.value = null
  try {
    await agentsStore.provisionAgent(props.agent.host_id, { index, tls_mode: 'auto' })
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : t('common.requestFailed')
  }
}

function submit() {
  emit('submit', { transport: { chain: chain.value } } satisfies AgentUpdatePayload)
}

watch(
  () => [props.visible, props.agent] as const,
  ([visible, agent]) => {
    if (!visible) return
    chain.value = cloneChain(agent)
    actionError.value = null
    Object.keys(probeResults).forEach(key => delete probeResults[Number(key)])
  },
  { immediate: true },
)
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <section class="settings-modal agent-config-modal">
      <header class="settings-modal-header">
        <h2 class="settings-modal-title">{{ t('settings.agents.editConnection') }}</h2>
      </header>
      <div class="settings-modal-body agent-config-body">
        <div class="chain-toolbar">
          <button type="button" class="settings-btn" data-test="transport-add-direct" @click="addEntry('direct')">direct</button>
          <button type="button" class="settings-btn" data-test="transport-add-tunnel" @click="addEntry('tunnel')">tunnel</button>
        </div>
        <div v-if="actionError" class="settings-alert settings-alert-danger">{{ actionError }}</div>

        <section
          v-for="(entry, index) in chain"
          :key="`${entry.type}-${index}`"
          class="transport-entry"
          :data-test="`transport-entry-${index}`"
        >
          <header class="transport-entry-head">
            <strong>{{ index + 1 }}. {{ entry.type }}</strong>
            <span v-if="index === 0" class="priority-badge">{{ t('settings.agents.primaryTransport') }}</span>
            <span v-else class="priority-badge degraded">{{ t('settings.agents.fallbackTransport') }}</span>
            <button type="button" class="icon-btn" :data-test="`transport-move-up-${index}`" @click="moveEntry(index, -1)">↑</button>
            <button type="button" class="icon-btn" :data-test="`transport-move-down-${index}`" @click="moveEntry(index, 1)">↓</button>
            <button type="button" class="icon-btn danger" :data-test="`transport-remove-${index}`" @click="removeEntry(index)">×</button>
          </header>

          <template v-if="entry.type === 'direct' && entry.direct">
            <div class="settings-field">
              <label class="settings-field-label">{{ t('settings.agents.directAddress') }}</label>
              <input v-model="entry.direct.address" class="settings-input" placeholder="100.64.0.8:57017" />
            </div>
            <label class="inline-check">
              <input v-model="entry.direct.tls" type="checkbox" />
              {{ t('settings.agents.useTLS') }}
            </label>
            <div class="settings-field">
              <label class="settings-field-label">{{ t('settings.agents.caCert') }}</label>
              <textarea v-model="entry.direct.ca_cert" class="settings-input cert-box" />
            </div>
          </template>

          <template v-if="entry.type === 'tunnel' && entry.tunnel">
            <div class="row">
              <div class="settings-field flex">
                <label class="settings-field-label">{{ t('settings.hostForm.sshAddress') }}</label>
                <input v-model="entry.tunnel.ssh_host" class="settings-input" />
              </div>
              <div class="settings-field port">
                <label class="settings-field-label">{{ t('settings.hostForm.port') }}</label>
                <input v-model.number="entry.tunnel.ssh_port" class="settings-input" type="number" min="1" />
              </div>
            </div>
            <div class="settings-field">
              <label class="settings-field-label">{{ t('settings.hostForm.sshUser') }}</label>
              <input v-model="entry.tunnel.ssh_user" class="settings-input" />
            </div>
            <div class="settings-field">
              <label class="settings-field-label">{{ t('settings.hostForm.remoteAgentPort') }}</label>
              <input v-model.number="entry.tunnel.remote_agent_port" class="settings-input" type="number" min="1" />
            </div>
          </template>

          <footer class="transport-entry-actions">
            <button type="button" class="settings-btn" :data-test="`transport-test-${index}`" @click="testEntry(index)">
              {{ t('settings.agents.testTransport') }}
            </button>
            <button
              v-if="props.agent?.security.provision_state === 'pending-bootstrap'"
              type="button"
              class="settings-btn settings-btn-secondary"
              :data-test="`transport-provision-${index}`"
              @click="provisionEntry(index)"
            >
              {{ t('settings.agents.provisionSecurity') }}
            </button>
            <span v-if="probeResults[index]" class="probe-result">{{ probeResults[index]?.status }}</span>
          </footer>
        </section>
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
.row {
  display: flex;
  gap: 8px;
}
.flex { flex: 1; }
.port { width: 90px; }
</style>
