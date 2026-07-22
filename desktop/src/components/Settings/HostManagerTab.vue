<!--
HostManagerTab：设置页 Host 身份管理标签页。

职责：
  - 列出远程 Host 的身份、地址元数据和 tag
  - 提供 Host 新建、编辑、删除入口
  - 展示 SSH 登录方式摘要

边界：
  - 不编辑 Agent 连接方式，Agent 配置由 AgentManagerTab 负责
  - 不建立隧道 WebSocket
  - 不执行 Agent 安装、卸载或探活
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRemoteStore } from '@/stores/remote'
import { tagColor } from '@/lib/tagColor'
import HostFormModal from './HostFormModal.vue'
import type { Host, HostCreatePayload } from '@/api/agent'

const store = useRemoteStore()
const { t } = useI18n()

const formVisible = ref(false)
const editing = ref<Host | null>(null)
const error = ref<string | null>(null)

const sortedHosts = computed(() =>
  [...store.hosts].sort((a, b) => a.name.localeCompare(b.name)),
)

onMounted(async () => {
  try {
    await store.loadHosts()
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('settings.hosts.loadFailed')
  }
})

function openCreate() {
  error.value = null
  editing.value = null
  formVisible.value = true
}

function openEdit(host: Host) {
  error.value = null
  editing.value = host
  formVisible.value = true
}

async function handleSubmit(payload: HostCreatePayload) {
  error.value = null
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

function addressLabel(host: Host): string {
  const parts = [host.public_ip, host.private_ip].filter(Boolean)
  return parts.length > 0 ? parts.join(' / ') : '-'
}

function sshLabel(host: Host): string {
  if (!host.ssh_host && !host.ssh_user) return '-'
  const user = host.ssh_user ? `${host.ssh_user}@` : ''
  const port = host.ssh_port ? `:${host.ssh_port}` : ''
  return `${user}${host.ssh_host || '-'}${port}`
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
            <th>{{ t('settings.hostForm.sshAddress') }}</th>
            <th>{{ t('settings.hosts.tags') }}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="host in sortedHosts" :key="host.id" data-test="host-row">
            <td>{{ host.name }}</td>
            <td>
              <div class="address-meta" data-test="host-address-meta">{{ addressLabel(host) }}</div>
            </td>
            <td>
              <div class="address-meta mono" data-test="host-ssh-meta">{{ sshLabel(host) }}</div>
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
            <td class="row-actions" data-test="host-row-actions">
              <button class="settings-btn settings-btn-text" data-test="host-edit" @click="openEdit(host)">{{ t('common.edit') }}</button>
              <button class="settings-btn settings-btn-text settings-btn-danger" data-test="host-delete" @click="handleDelete(host)">{{ t('common.delete') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="settings-empty">{{ t('settings.hosts.empty') }}</div>

    <HostFormModal
      :visible="formVisible"
      :initial="editing"
      :error="error"
      @submit="handleSubmit"
      @cancel="formVisible = false"
    />
  </section>
</template>

<style scoped>
.host-manager {
  width: 100%;
}
.address-meta {
  color: var(--text-secondary);
  font-size: 12px;
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
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  white-space: nowrap;
}
</style>
