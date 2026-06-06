<!--
HostManagerTab：设置页主机管理标签页。

职责：
  - 列出所有远程 Host 及其 SSH、tag 和隧道状态
  - 提供 Host 新建、编辑、删除入口
  - 触发远端 Agent 安装/重装并展示结果

边界：
  - 不管理 LogSource，监听任务由 Sidebar 负责
  - 不渲染日志面板
-->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'
import { useRemoteStore } from '@/stores/remote'
import { tagColor } from '@/lib/tagColor'
import { formatRelativeAge } from '@/lib/timeDisplay'
import { WS_BASE, type TunnelStatus } from '@/api/agent'
import HostFormModal from './HostFormModal.vue'
import type { Host, HostCreatePayload, HostManagedDeploymentStatus, ManagedCollectorStatus } from '@/api/agent'

const store = useRemoteStore()
const { t } = useI18n()

const formVisible = ref(false)
const editing = ref<Host | null>(null)
const error = ref<string | null>(null)
const expandedErrors = ref<Set<string>>(new Set())
const installingHostIds = ref<Set<string>>(new Set())
const checkingHostIds = ref<Set<string>>(new Set())
const uninstallingHostIds = ref<Set<string>>(new Set())
const checkingManagedHostIds = ref<Set<string>>(new Set())
const managedStatuses = ref<Map<string, HostManagedDeploymentStatus>>(new Map())
const installMessages = ref<Map<string, string>>(new Map())
const installErrors = ref<Map<string, string>>(new Map())
const refreshingAgents = ref(false)
const uninstallTarget = ref<Host | null>(null)
const uninstallRemoveData = ref(false)

const sortedHosts = computed(() =>
  [...store.hosts].sort((a, b) => a.name.localeCompare(b.name)),
)

const remoteHosts = computed(() => sortedHosts.value.filter(host => !host.is_self))

let tunnelWs: WebSocket | null = null
let agentCheckTimer: ReturnType<typeof setInterval> | null = null
const agentCheckIntervalMs = 60_000

function connectTunnelWs() {
  tunnelWs = new WebSocket(`${WS_BASE}/ws/tunnels`)
  tunnelWs.onmessage = (event) => {
    try {
      const status = JSON.parse(event.data) as TunnelStatus
      store.applyTunnelUpdate(status)
    } catch {
      // 忽略非法帧
    }
  }
  tunnelWs.onclose = () => { tunnelWs = null }
}

onMounted(async () => {
  try {
    await Promise.all([store.loadHosts(), store.loadTunnels()])
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.hosts.loadFailed')
  }
  connectTunnelWs()
  await refreshAllAgents()
  agentCheckTimer = setInterval(() => {
    void refreshAllAgents()
  }, agentCheckIntervalMs)
})

onUnmounted(() => {
  if (agentCheckTimer) {
    clearInterval(agentCheckTimer)
    agentCheckTimer = null
  }
  tunnelWs?.close()
})

function openCreate() {
  editing.value = null
  formVisible.value = true
}

function openEdit(host: Host) {
  editing.value = host
  formVisible.value = true
}

async function handleSubmit(payload: HostCreatePayload) {
  try {
    if (editing.value) {
      await store.updateHost(editing.value.id, payload)
    } else {
      await store.createHost(payload)
    }
    formVisible.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.hosts.saveFailed')
  }
}

async function handleDelete(host: Host) {
  if (!confirm(t('settings.hosts.deleteConfirm', { name: host.name }))) return
  try {
    await store.deleteHost(host.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.hosts.deleteFailed')
  }
}

function tunnelLabel(hostId: string): string {
  const status = store.tunnelOf(hostId)
  if (!status?.state) return '-'
  if (status.state === 'open' && status.local_port) return `open :${status.local_port}`
  if (status.state === 'failed' && status.error) {
    const brief = status.error.length > 40 ? status.error.slice(0, 40) + '...' : status.error
    return `failed: ${brief}`
  }
  return status.state
}

function agentHealthLabel(hostId: string): string {
  const status = store.tunnelOf(hostId)
  // 隧道未连接时不显示 agent 状态（agent 状态依附隧道连通）
  if (!status || !status.state || status.state !== 'open') return ''
  return status.agent ?? 'unknown'
}

function agentHealthClass(hostId: string): string {
  const agent = store.tunnelOf(hostId)?.agent
  if (agent === 'unreachable' || agent === 'version-mismatch') return 'agent-health-bad'
  if (agent === 'healthy') return 'agent-health-ok'
  return ''
}

function agentVersionLabel(hostId: string): string {
  const version = store.tunnelOf(hostId)?.agent_version?.trim()
  if (!version) return ''
  return version.startsWith('v') ? version : `v${version}`
}

function agentCheckedLabel(hostId: string): string {
  return formatRelativeAge(
    store.tunnelOf(hostId)?.agent_checked_at,
    count => t('settings.hosts.checkedSecondsAgo', { count }),
    count => t('settings.hosts.checkedMinutesAgo', { count }),
    count => t('settings.hosts.checkedHoursAgo', { count }),
  )
}

function agentMetaLabel(hostId: string): string {
  const parts = [agentVersionLabel(hostId), agentCheckedLabel(hostId)].filter(Boolean)
  return parts.length > 0 ? parts.join(' · ') : t('settings.hosts.agentMetaEmpty')
}

function managedStatusOf(hostId: string): HostManagedDeploymentStatus | undefined {
  return managedStatuses.value.get(hostId)
}

function shortError(message: string): string {
  return message.length > 52 ? message.slice(0, 52) + '...' : message
}

function runningCollectorCount(status: HostManagedDeploymentStatus): number {
  return status.remote?.collectors?.filter(item => item.running && item.status !== 'failed').length ?? 0
}

function managedStatusLabel(hostId: string): string {
  const status = managedStatusOf(hostId)
  if (!status) return t('settings.hosts.orchestrationUnchecked')
  if (!status.tunnel_connected) {
    return t('settings.hosts.orchestrationDisconnected', { count: status.desired_deployment_count })
  }
  if (status.error && !status.remote) {
    return t('settings.hosts.orchestrationError', { message: shortError(status.error) })
  }
  const remote = status.remote
  if (!remote) return t('settings.hosts.orchestrationChecking')
  return t('settings.hosts.orchestrationSummary', {
    deployments: remote.deployment_count,
    desired: status.desired_deployment_count,
    collectors: runningCollectorCount(status),
    desiredCollectors: status.desired_collector_count,
  })
}

function collectorIssue(item: ManagedCollectorStatus): string {
  if (item.error) return item.error
  if (!item.running) return t('settings.hosts.collectorNotRunning')
  if (item.status === 'failed') return 'failed'
  return ''
}

function managedIssueLabel(hostId: string): string {
  const status = managedStatusOf(hostId)
  if (!status) return ''
  if (status.error && status.tunnel_connected) {
    return t('settings.hosts.orchestrationIssue', { target: status.host_name || status.host_id, detail: shortError(status.error) })
  }
  const issue = status.remote?.collectors?.find(item => collectorIssue(item))
  if (!issue) return ''
  const target = issue.service_name || issue.deployment_id
  return t('settings.hosts.orchestrationIssue', { target, detail: shortError(collectorIssue(issue)) })
}

function hasManagedIssue(hostId: string): boolean {
  return managedIssueLabel(hostId) !== ''
}

function hasDetectedAgent(hostId: string): boolean {
  const status = store.tunnelOf(hostId)
  return Boolean(
    status?.agent_version?.trim()
    || status?.agent === 'healthy'
    || status?.agent === 'version-mismatch',
  )
}

function toggleError(hostId: string) {
  const next = new Set(expandedErrors.value)
  if (next.has(hostId)) next.delete(hostId)
  else next.add(hostId)
  expandedErrors.value = next
}

function tunnelError(hostId: string): string {
  return store.tunnelOf(hostId)?.error ?? ''
}

function isFailed(hostId: string): boolean {
  return store.tunnelOf(hostId)?.state === 'failed'
}

function isInstalling(hostId: string): boolean {
  return installingHostIds.value.has(hostId)
}

function isChecking(hostId: string): boolean {
  return checkingHostIds.value.has(hostId)
}

function isUninstalling(hostId: string): boolean {
  return uninstallingHostIds.value.has(hostId)
}

function isCheckingManaged(hostId: string): boolean {
  return checkingManagedHostIds.value.has(hostId)
}

function installActionLabel(hostId: string): string {
  if (isInstalling(hostId)) return t('settings.hosts.installing')
  return hasDetectedAgent(hostId) ? t('settings.hosts.reinstallAction') : t('settings.hosts.installAction')
}

function installMessage(hostId: string): string {
  return installMessages.value.get(hostId) || ''
}

function hostError(hostId: string): string {
  return installErrors.value.get(hostId) || tunnelError(hostId)
}

function hasHostError(hostId: string): boolean {
  return installErrors.value.has(hostId) || isFailed(hostId)
}

function deleteHostError(hostId: string) {
  const next = new Map(installErrors.value)
  next.delete(hostId)
  installErrors.value = next
}

function setHostError(hostId: string, message: string) {
  const next = new Map(installErrors.value)
  next.set(hostId, message)
  installErrors.value = next
  const expanded = new Set(expandedErrors.value)
  expanded.add(hostId)
  expandedErrors.value = expanded
}

async function checkAgent(host: Host) {
  if (host.is_self) return
  const checking = new Set(checkingHostIds.value)
  checking.add(host.id)
  checkingHostIds.value = checking
  try {
    await store.checkHostAgent(host.id)
    await refreshManagedStatus(host)
    deleteHostError(host.id)
  } catch (err) {
    setHostError(host.id, err instanceof Error ? err.message : t('settings.hosts.agentCheckFailed'))
  } finally {
    const next = new Set(checkingHostIds.value)
    next.delete(host.id)
    checkingHostIds.value = next
  }
}

async function refreshManagedStatus(host: Host) {
  if (host.is_self || isCheckingManaged(host.id)) return
  const checking = new Set(checkingManagedHostIds.value)
  checking.add(host.id)
  checkingManagedHostIds.value = checking
  try {
    const status = await store.getHostManagedDeploymentStatus(host.id)
    const next = new Map(managedStatuses.value)
    next.set(host.id, status)
    managedStatuses.value = next
  } catch (err) {
    const next = new Map(managedStatuses.value)
    next.set(host.id, {
      host_id: host.id,
      host_name: host.name,
      desired_deployment_count: 0,
      desired_collector_count: 0,
      tunnel_connected: false,
      error: err instanceof Error ? err.message : t('settings.hosts.orchestrationCheckFailed'),
    })
    managedStatuses.value = next
  } finally {
    const next = new Set(checkingManagedHostIds.value)
    next.delete(host.id)
    checkingManagedHostIds.value = next
  }
}

async function refreshAllAgents() {
  if (refreshingAgents.value) return
  refreshingAgents.value = true
  try {
    await Promise.all(remoteHosts.value.map(host => checkAgent(host)))
  } finally {
    refreshingAgents.value = false
  }
}

async function installAgent(host: Host) {
  const installing = new Set(installingHostIds.value)
  installing.add(host.id)
  installingHostIds.value = installing

  deleteHostError(host.id)

  try {
    const result = await store.installHostAgent(host.id)
    const messages = new Map(installMessages.value)
    messages.set(host.id, t('settings.hosts.installed', { platform: result.platform }))
    installMessages.value = messages
    await store.checkHostAgent(host.id)
  } catch (err) {
    setHostError(host.id, err instanceof Error ? err.message : t('settings.hosts.installFailed'))
  } finally {
    const next = new Set(installingHostIds.value)
    next.delete(host.id)
    installingHostIds.value = next
  }
}

function openUninstall(host: Host) {
  uninstallTarget.value = host
  uninstallRemoveData.value = false
}

function closeUninstall() {
  uninstallTarget.value = null
  uninstallRemoveData.value = false
}

async function confirmUninstall() {
  const host = uninstallTarget.value
  if (!host) return
  const uninstalling = new Set(uninstallingHostIds.value)
  uninstalling.add(host.id)
  uninstallingHostIds.value = uninstalling
  deleteHostError(host.id)

  try {
    const result = await store.uninstallHostAgent(host.id, uninstallRemoveData.value)
    const messages = new Map(installMessages.value)
    messages.set(host.id, result.removed_data ? t('settings.hosts.uninstalledWithData') : t('settings.hosts.uninstalledKeepData'))
    installMessages.value = messages
    closeUninstall()
  } catch (err) {
    setHostError(host.id, err instanceof Error ? err.message : t('settings.hosts.uninstallFailed'))
  } finally {
    const next = new Set(uninstallingHostIds.value)
    next.delete(host.id)
    uninstallingHostIds.value = next
  }
}
</script>

<template>
  <section class="host-manager">
    <header class="settings-pane-header">
      <div>
        <h1 class="settings-pane-title">{{ t('settings.hosts.title') }}</h1>
      </div>
      <div class="settings-toolbar">
        <button class="settings-btn settings-btn-primary" data-test="host-add" @click="openCreate">+ {{ t('settings.hosts.add') }}</button>
      </div>
    </header>

    <div v-if="error" class="settings-alert settings-alert-danger">{{ error }}</div>
    <div v-if="sortedHosts.length > 0" class="settings-surface settings-surface-scroll">
      <table class="settings-table host-table">
        <thead>
          <tr>
            <th>{{ t('settings.hosts.name') }}</th>
            <th>{{ t('settings.hosts.address') }}</th>
            <th>{{ t('settings.hosts.tags') }}</th>
            <th>{{ t('settings.hosts.tunnel') }}</th>
            <th>{{ t('settings.hosts.agent') }}</th>
            <th class="agent-meta-heading">
              <div class="agent-meta-header">
                <span>{{ t('settings.hosts.agentMeta') }}</span>
                <button
                  class="settings-btn settings-btn-icon settings-btn-ghost agent-refresh-button"
                  type="button"
                  :disabled="refreshingAgents"
                  :title="t('settings.hosts.agentRefreshTitle')"
                  :aria-label="t('settings.hosts.agentRefreshTitle')"
                  data-test="agent-refresh-all"
                  @click="refreshAllAgents"
                >
                  <Icon icon="lucide:refresh-cw" aria-hidden="true" />
                </button>
              </div>
            </th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="host in sortedHosts" :key="host.id">
            <tr data-test="host-row">
              <td>{{ host.name }}</td>
              <td>
                <div class="mono">{{ host.ssh_user }}@{{ host.ssh_host }}:{{ host.ssh_port }}</div>
                <div v-if="host.public_ip || host.private_ip" class="address-meta" data-test="host-address-meta">
                  <span v-if="host.public_ip">{{ t('settings.hostForm.publicIP') }} {{ host.public_ip }}</span>
                  <span v-if="host.private_ip">{{ t('settings.hostForm.privateIP') }} {{ host.private_ip }}</span>
                </div>
              </td>
              <td>
                <span
                  v-for="tag in host.tags"
                  :key="tag"
                  class="tag-chip"
                  :style="{ background: tagColor(tag) }"
                >
                  {{ tag }}
                </span>
              </td>
              <td
                class="mono tunnel-cell"
                :class="{ 'tunnel-failed': hasHostError(host.id) }"
                @click="hasHostError(host.id) && toggleError(host.id)"
              >
                {{ tunnelLabel(host.id) }}
                <span v-if="hasHostError(host.id)" class="expand-icon">{{ expandedErrors.has(host.id) ? '▴' : '▾' }}</span>
              </td>
              <td class="agent-cell">
                <div class="agent-stack">
                  <div class="agent-action-row" data-test="host-agent-actions">
                    <span
                      v-if="agentHealthLabel(host.id)"
                      class="agent-health-badge"
                      :class="agentHealthClass(host.id)"
                      data-test="agent-health"
                    >
                      {{ agentHealthLabel(host.id) }}
                    </span>
                    <span v-if="installMessage(host.id)" class="agent-ok">{{ installMessage(host.id) }}</span>
                    <button
                      v-if="!host.is_self"
                      class="settings-btn settings-btn-text"
                      type="button"
                      :disabled="isInstalling(host.id) || isChecking(host.id)"
                      :title="t('settings.hosts.agentInstallHelp')"
                      data-test="host-install-agent"
                      @click="installAgent(host)"
                    >
                      {{ installActionLabel(host.id) }}
                    </button>
                    <button
                      v-if="!host.is_self"
                      class="settings-btn settings-btn-text settings-btn-danger"
                      type="button"
                      :disabled="isUninstalling(host.id)"
                      :title="t('settings.hosts.uninstallAction')"
                      data-test="host-uninstall-agent"
                      @click="openUninstall(host)"
                    >
                      {{ isUninstalling(host.id) ? t('settings.hosts.uninstalling') : t('settings.hosts.uninstallAction') }}
                    </button>
                  </div>
                  <span v-if="!host.is_self" class="install-help install-help-row" data-test="host-install-help">
                    {{ t('settings.hosts.agentInstallHelp') }}
                  </span>
                  <span
                    v-if="!host.is_self"
                    class="orchestration-line"
                    :class="{ 'orchestration-line-bad': hasManagedIssue(host.id) }"
                    data-test="host-managed-status"
                  >
                    {{ managedStatusLabel(host.id) }}
                  </span>
                  <span
                    v-if="managedIssueLabel(host.id)"
                    class="orchestration-issue"
                    data-test="host-managed-issue"
                  >
                    {{ managedIssueLabel(host.id) }}
                  </span>
                </div>
              </td>
              <td class="agent-meta-cell" data-test="agent-meta">
                <div class="agent-meta-row">
                  <span class="agent-meta-text">{{ agentMetaLabel(host.id) }}</span>
                  <button
                    v-if="!host.is_self"
                    class="settings-btn settings-btn-icon settings-btn-ghost agent-refresh-button"
                    type="button"
                    :disabled="isChecking(host.id)"
                    :title="t('settings.hosts.agentRefreshTitle')"
                    :aria-label="t('settings.hosts.agentRefreshTitle')"
                    :data-test="`host-refresh-agent-${host.id}`"
                    @click="checkAgent(host)"
                  >
                    <Icon icon="lucide:refresh-cw" aria-hidden="true" />
                  </button>
                </div>
              </td>
              <td class="row-actions" data-test="host-row-actions">
                <button class="settings-btn settings-btn-text" data-test="host-edit" @click="openEdit(host)">{{ t('common.edit') }}</button>
                <button class="settings-btn settings-btn-text settings-btn-danger" data-test="host-delete" @click="handleDelete(host)">{{ t('common.delete') }}</button>
              </td>
            </tr>
            <tr v-if="hasHostError(host.id) && expandedErrors.has(host.id)" class="error-row" data-test="host-error-row">
              <td colspan="7">
                <div class="tunnel-error-detail">{{ hostError(host.id) }}</div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
    <div v-else class="settings-empty">{{ t('settings.hosts.empty') }}</div>

    <HostFormModal
      :visible="formVisible"
      :initial="editing"
      @submit="handleSubmit"
      @cancel="formVisible = false"
    />

    <div v-if="uninstallTarget" class="settings-modal-backdrop" data-test="agent-uninstall-modal" @click.self="closeUninstall">
      <section class="settings-modal">
        <header class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.hosts.uninstallTitle', { name: uninstallTarget.name }) }}</h2>
          <button type="button" class="settings-btn settings-btn-icon settings-btn-ghost" @click="closeUninstall">×</button>
        </header>
        <div class="settings-modal-body uninstall-body">
          <p>{{ t('settings.hosts.uninstallBody') }}</p>
          <label class="uninstall-data-option">
            <input v-model="uninstallRemoveData" type="checkbox" data-test="agent-uninstall-remove-data" />
            <span>{{ t('settings.hosts.uninstallRemoveData') }}</span>
          </label>
          <p class="uninstall-note">{{ t('settings.hosts.uninstallKeepData') }}</p>
        </div>
        <footer class="settings-modal-footer">
          <button type="button" class="settings-btn settings-btn-secondary" @click="closeUninstall">{{ t('common.cancel') }}</button>
          <button
            type="button"
            class="settings-btn settings-btn-danger"
            :disabled="isUninstalling(uninstallTarget.id)"
            data-test="agent-uninstall-confirm"
            @click="confirmUninstall"
          >
            {{ isUninstalling(uninstallTarget.id) ? t('settings.hosts.uninstalling') : t('settings.hosts.uninstallConfirm') }}
          </button>
        </footer>
      </section>
    </div>
  </section>
</template>

<style scoped>
.host-manager {
  width: 100%;
}
.mono {
  font-family: var(--font-mono, monospace);
}
.address-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 10px;
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
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  white-space: nowrap;
}
.tunnel-cell {
  white-space: nowrap;
}
.agent-cell {
  min-width: 420px;
}
.agent-stack {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.agent-action-row {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.agent-ok {
  color: var(--status-running);
  font-size: 11px;
}
.agent-health-badge {
  font-size: 11px;
}
.agent-health-ok {
  color: var(--status-running);
}
.agent-health-bad {
  color: var(--status-failed);
}
.agent-meta-cell {
  color: var(--text-tertiary);
  white-space: nowrap;
}
.host-table th.agent-meta-heading {
  vertical-align: middle;
}
.agent-meta-header {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 24px;
  line-height: 1;
  white-space: nowrap;
}
.agent-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 24px;
}
.agent-meta-text {
  min-width: 0;
}
.agent-refresh-button {
  display: inline-flex;
  flex: 0 0 24px;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  min-height: 24px;
  padding: 0;
  line-height: 1;
  vertical-align: middle;
}
.agent-refresh-button :deep(svg) {
  width: 14px;
  height: 14px;
}
.install-help {
  max-width: 300px;
  color: var(--text-tertiary);
  font-size: 11px;
  white-space: normal;
}
.install-help-row {
  display: block;
  max-width: 420px;
  line-height: 1.35;
}
.orchestration-line {
  display: block;
  max-width: 420px;
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 1.35;
}
.orchestration-line-bad,
.orchestration-issue {
  color: var(--status-failed);
}
.orchestration-issue {
  display: block;
  max-width: 420px;
  font-size: 11px;
  line-height: 1.35;
  word-break: break-word;
}
.tunnel-failed {
  color: var(--status-failed);
  cursor: pointer;
}
.expand-icon {
  margin-left: 4px;
  font-size: 9px;
  color: var(--text-tertiary);
}
.error-row td {
  padding: 0;
  border-bottom: 1px solid var(--border-secondary);
}
.tunnel-error-detail {
  padding: 6px 12px;
  color: var(--status-failed);
  background: rgba(248, 81, 73, 0.06);
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  word-break: break-all;
  white-space: pre-wrap;
}
.uninstall-body {
  display: grid;
  gap: 10px;
}
.uninstall-body p {
  margin: 0;
}
.uninstall-data-option {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-primary);
  font-size: 13px;
}
.uninstall-note {
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
