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
import { useRemoteStore } from '@/stores/remote'
import { tagColor } from '@/lib/tagColor'
import { WS_BASE, type TunnelStatus } from '@/api/agent'
import HostFormModal from './HostFormModal.vue'
import type { Host, HostCreatePayload } from '@/api/agent'

const store = useRemoteStore()
const { t } = useI18n()

const formVisible = ref(false)
const editing = ref<Host | null>(null)
const error = ref<string | null>(null)
const expandedErrors = ref<Set<string>>(new Set())
const installingHostIds = ref<Set<string>>(new Set())
const installMessages = ref<Map<string, string>>(new Map())
const installErrors = ref<Map<string, string>>(new Map())

const sortedHosts = computed(() =>
  [...store.hosts].sort((a, b) => a.name.localeCompare(b.name)),
)

let tunnelWs: WebSocket | null = null

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
})

onUnmounted(() => {
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

function installMessage(hostId: string): string {
  return installMessages.value.get(hostId) || ''
}

function hostError(hostId: string): string {
  return installErrors.value.get(hostId) || tunnelError(hostId)
}

function hasHostError(hostId: string): boolean {
  return installErrors.value.has(hostId) || isFailed(hostId)
}

async function installAgent(host: Host) {
  const installing = new Set(installingHostIds.value)
  installing.add(host.id)
  installingHostIds.value = installing

  const errors = new Map(installErrors.value)
  errors.delete(host.id)
  installErrors.value = errors

  try {
    const result = await store.installHostAgent(host.id)
    const messages = new Map(installMessages.value)
    messages.set(host.id, t('settings.hosts.installed', { platform: result.platform }))
    installMessages.value = messages
    await store.loadTunnels()
  } catch (err) {
    const next = new Map(installErrors.value)
    next.set(host.id, err instanceof Error ? err.message : t('settings.hosts.installFailed'))
    installErrors.value = next
    const expanded = new Set(expandedErrors.value)
    expanded.add(host.id)
    expandedErrors.value = expanded
  } finally {
    const next = new Set(installingHostIds.value)
    next.delete(host.id)
    installingHostIds.value = next
  }
}
</script>

<template>
  <section class="host-manager">
    <header class="pane-header">
      <h1>{{ t('settings.hosts.title') }}</h1>
      <div class="toolbar">
        <button class="primary" data-test="host-add" @click="openCreate">+ {{ t('settings.hosts.add') }}</button>
      </div>
    </header>

    <div v-if="error" class="error">{{ error }}</div>
    <table v-if="sortedHosts.length > 0" class="host-table">
      <thead>
        <tr>
          <th>{{ t('settings.hosts.name') }}</th>
          <th>{{ t('settings.hosts.address') }}</th>
          <th>{{ t('settings.hosts.tags') }}</th>
          <th>{{ t('settings.hosts.tunnel') }}</th>
          <th>{{ t('settings.hosts.agent') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="host in sortedHosts" :key="host.id">
          <tr data-test="host-row">
            <td>{{ host.name }}</td>
            <td class="mono">{{ host.ssh_user }}@{{ host.ssh_host }}:{{ host.ssh_port }}</td>
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
              <span v-if="installMessage(host.id)" class="agent-ok">{{ installMessage(host.id) }}</span>
              <button
                v-if="!host.is_self"
                type="button"
                :disabled="isInstalling(host.id)"
                data-test="host-install-agent"
                @click="installAgent(host)"
              >
                {{ isInstalling(host.id) ? t('settings.hosts.installing') : t('settings.hosts.installAction') }}
              </button>
            </td>
            <td class="row-actions">
              <button @click="openEdit(host)">{{ t('common.edit') }}</button>
              <button class="danger" @click="handleDelete(host)">{{ t('common.delete') }}</button>
            </td>
          </tr>
          <tr v-if="hasHostError(host.id) && expandedErrors.has(host.id)" class="error-row" data-test="host-error-row">
            <td colspan="6">
              <div class="tunnel-error-detail">{{ hostError(host.id) }}</div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <div v-else class="empty">{{ t('settings.hosts.empty') }}</div>

    <HostFormModal
      :visible="formVisible"
      :initial="editing"
      @submit="handleSubmit"
      @cancel="formVisible = false"
    />
  </section>
</template>

<style scoped>
.host-manager {
  width: 100%;
}
.pane-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
h1 {
  margin: 0;
  font-size: 18px;
}
.toolbar {
  display: flex;
  gap: 8px;
}
.toolbar button {
  padding: 5px 9px;
  color: var(--text-primary);
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 5px;
  cursor: pointer;
  font-size: 11px;
}
.toolbar button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
.error {
  padding: 6px 10px;
  margin-bottom: 8px;
  color: var(--status-failed);
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.3);
  font-size: 11px;
}
.host-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.host-table th,
.host-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
}
.host-table th {
  color: var(--text-tertiary);
  font-weight: 400;
  font-size: 11px;
}
.mono {
  font-family: var(--font-mono, monospace);
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
  white-space: nowrap;
}
.row-actions button {
  padding: 0 4px;
  color: var(--accent);
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 11px;
}
.row-actions button.danger {
  color: var(--status-failed);
}
.empty {
  padding: 32px;
  color: var(--text-tertiary);
  text-align: center;
  font-size: 12px;
}
.tunnel-cell {
  white-space: nowrap;
}
.agent-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.agent-cell button {
  padding: 0 4px;
  color: var(--accent);
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 11px;
}
.agent-cell button:disabled {
  color: var(--text-tertiary);
  cursor: default;
}
.agent-ok {
  color: var(--status-running);
  font-size: 11px;
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
</style>
