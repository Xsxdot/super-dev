<!--
AgentManagerTab：设置页 Agent 连接与安装管理标签页。

职责：
  - 列出 Host Agent 的连接方式和运行态
  - 提供连接方式编辑、安装命令生成、探活与移除入口
  - 复用 NodeRegistry 前端缓存展示最新 runtime

边界：
  - 不编辑 Host 身份字段
  - 不管理项目或日志状态
  - 不直接打开 deployment 运行控制
-->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, type AgentDTO, type AgentRuntime, type AgentUpdatePayload } from '@/api/agent'
import { useAgentsStore } from '@/stores/agents'
import { useNodeStore } from '@/stores/node'
import { tagColor } from '@/lib/tagColor'
import { formatRelativeAge } from '@/lib/timeDisplay'
import AgentConfigModal from './AgentConfigModal.vue'
import AgentInstallModal from './AgentInstallModal.vue'

const agentsStore = useAgentsStore()
const nodeStore = useNodeStore()
const { t } = useI18n()

const configTarget = ref<AgentDTO | null>(null)
const installTarget = ref<AgentDTO | null>(null)
const checking = ref<Set<string>>(new Set())
const installing = ref<Set<string>>(new Set())
const uninstalling = ref<Set<string>>(new Set())
const error = ref<string | null>(null)

const sortedAgents = computed(() =>
  [...agentsStore.agents].sort((a, b) => a.host_name.localeCompare(b.host_name) || a.host_id.localeCompare(b.host_id)),
)

onMounted(async () => {
  try {
    await Promise.all([agentsStore.loadAgents(), nodeStore.start()])
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.loadFailed')
  }
})

onUnmounted(() => {
  nodeStore.stop()
})

function runtimeOf(agent: AgentDTO): AgentRuntime {
  return nodeStore.agentRuntimeOf(agent.host_id) ?? agent.runtime
}

function transportLabel(agent: AgentDTO): string {
  if (agent.transport.type === 'direct') {
    return agent.transport.direct?.address ? `direct · ${agent.transport.direct.address}` : 'direct'
  }
  if (agent.transport.type === 'tunnel') {
    const tunnel = agent.transport.tunnel
    return tunnel ? `tunnel · ${tunnel.ssh_user}@${tunnel.ssh_host}:${tunnel.ssh_port}` : 'tunnel'
  }
  return agent.transport.type
}

function updatedLabel(agent: AgentDTO): string {
  return formatRelativeAge(
    agent.updated_at,
    count => t('settings.hosts.checkedSecondsAgo', { count }),
    count => t('settings.hosts.checkedMinutesAgo', { count }),
    count => t('settings.hosts.checkedHoursAgo', { count }),
  ) || '-'
}

async function saveConfig(payload: AgentUpdatePayload) {
  if (!configTarget.value) return
  try {
    await agentsStore.updateAgent(configTarget.value.host_id, payload)
    configTarget.value = null
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.saveFailed')
  }
}

async function checkAgent(agent: AgentDTO) {
  const next = new Set(checking.value)
  next.add(agent.host_id)
  checking.value = next
  try {
    agentsStore.upsert(await api.checkAgent(agent.host_id))
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.checkFailed')
  } finally {
    const done = new Set(checking.value)
    done.delete(agent.host_id)
    checking.value = done
  }
}

async function installAgent(agent: AgentDTO) {
  const next = new Set(installing.value)
  next.add(agent.host_id)
  installing.value = next
  try {
    await api.installHostAgent(agent.host_id)
    await checkAgent(agent)
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.installFailed')
  } finally {
    const done = new Set(installing.value)
    done.delete(agent.host_id)
    installing.value = done
  }
}

async function uninstallAgent(agent: AgentDTO) {
  if (!confirm(t('settings.agents.uninstallConfirm', { name: agent.host_name }))) return
  const next = new Set(uninstalling.value)
  next.add(agent.host_id)
  uninstalling.value = next
  try {
    await api.uninstallHostAgent(agent.host_id, { remove_data: false })
    await agentsStore.loadAgents()
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.agents.uninstallFailed')
  } finally {
    const done = new Set(uninstalling.value)
    done.delete(agent.host_id)
    uninstalling.value = done
  }
}
</script>

<template>
  <section class="agent-manager">
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.agents.title') }}</h1>
      </div>
      <div class="settings-toolbar">
        <button class="settings-btn settings-btn-secondary" type="button" :disabled="agentsStore.loading" @click="agentsStore.loadAgents()">
          {{ t('settings.agents.refresh') }}
        </button>
      </div>
    </header>

    <div v-if="error || agentsStore.error" class="settings-alert settings-alert-danger">{{ error || agentsStore.error }}</div>
    <div v-if="sortedAgents.length > 0" class="settings-surface settings-surface-scroll">
      <table class="settings-table agent-table">
        <thead>
          <tr>
            <th>{{ t('settings.agents.host') }}</th>
            <th>{{ t('settings.hosts.tags') }}</th>
            <th>{{ t('settings.agents.transport') }}</th>
            <th>{{ t('settings.agents.health') }}</th>
            <th>{{ t('settings.agents.updated') }}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="agent in sortedAgents" :key="agent.host_id" data-test="agent-row">
            <td>
              <div>{{ agent.host_name }}</div>
              <div class="muted mono">{{ agent.host_id }}</div>
            </td>
            <td>
              <span
                v-for="tag in agent.tags"
                :key="tag"
                class="tag-chip"
                :style="{ background: tagColor(tag) }"
              >
                {{ tag }}
              </span>
            </td>
            <td class="mono">{{ transportLabel(agent) }}</td>
            <td>
              <span class="health" :class="`health-${runtimeOf(agent).health}`">{{ runtimeOf(agent).health }}</span>
              <span v-if="runtimeOf(agent).version" class="muted"> · v{{ runtimeOf(agent).version?.replace(/^v/, '') }}</span>
            </td>
            <td class="muted">{{ updatedLabel(agent) }}</td>
            <td class="row-actions">
              <button class="settings-btn settings-btn-text" type="button" :data-test="`agent-edit-${agent.host_id}`" @click="configTarget = agent">
                {{ t('settings.agents.editConnection') }}
              </button>
              <button class="settings-btn settings-btn-text" type="button" :disabled="installing.has(agent.host_id)" :data-test="`agent-install-${agent.host_id}`" @click="installAgent(agent)">
                {{ t('settings.agents.install') }}
              </button>
              <button class="settings-btn settings-btn-text" type="button" :data-test="`agent-generate-command-${agent.host_id}`" @click="installTarget = agent">
                {{ t('settings.agents.generateCommand') }}
              </button>
              <button class="settings-btn settings-btn-text" type="button" :disabled="checking.has(agent.host_id)" @click="checkAgent(agent)">
                {{ t('settings.agents.check') }}
              </button>
              <button class="settings-btn settings-btn-text settings-btn-danger" type="button" :disabled="uninstalling.has(agent.host_id)" @click="uninstallAgent(agent)">
                {{ t('settings.agents.uninstall') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="settings-empty">{{ t('settings.agents.empty') }}</div>

    <AgentConfigModal
      :visible="Boolean(configTarget)"
      :agent="configTarget"
      @submit="saveConfig"
      @cancel="configTarget = null"
    />
    <AgentInstallModal
      :visible="Boolean(installTarget)"
      :agent="installTarget"
      @cancel="installTarget = null"
    />
  </section>
</template>

<style scoped>
.agent-manager {
  width: 100%;
}
.mono {
  font-family: var(--font-mono, monospace);
}
.muted {
  color: var(--text-tertiary);
  font-size: 11px;
}
.tag-chip {
  display: inline-block;
  padding: 1px 6px;
  margin-right: 4px;
  color: #fff;
  border-radius: 2px;
  font-size: 10px;
}
.row-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
.health-healthy {
  color: var(--status-running);
}
.health-unreachable,
.health-version-mismatch {
  color: var(--status-failed);
}
</style>
