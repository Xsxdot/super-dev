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

const rescanTarget = ref<Host | null>(null)
const rescanFingerprint = ref('')
const rescanError = ref('')

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

// 重采是「接受新指纹」的唯一入口，且刻意与连接失败现场分离：
// 若把它做成报错弹窗里的一个按钮，用户会条件反射点掉，防护形同虚设。
async function openRescan(host: Host) {
  rescanTarget.value = host
  rescanFingerprint.value = ''
  rescanError.value = ''
  // 记录发起采集时的目标 host id，用于在异步返回时判断弹窗是否仍在展示同一台主机
  // （见下方成功/失败分支的 identity 校验）。
  const scannedHostId = host.id
  try {
    const result = await store.scanHostKey({
      ssh_host: host.ssh_host ?? '',
      ssh_port: host.ssh_port || 22,
    })
    // 采集到的指纹只对发起采集的那台主机有意义：飞行期间用户若关闭弹窗改采别的
    // host，此时绝不能把 A 的指纹套用到 B 的弹窗上，否则会把错误的指纹当作「用户已确认」
    // 写入 B——这正是 fail-closed 设计要防止的失败模式，且发生得悄无声息。
    if (rescanTarget.value?.id !== scannedHostId) return
    rescanFingerprint.value = result.fingerprint
  } catch (err) {
    if (rescanTarget.value?.id !== scannedHostId) return
    rescanError.value = err instanceof Error ? err.message : String(err)
  }
}

async function confirmRescan() {
  const host = rescanTarget.value
  if (!host || !rescanFingerprint.value) return
  try {
    // store.updateHost 内部已回写 hosts 状态，无需再手动刷新列表。
    await store.updateHost(host.id, {
      name: host.name,
      public_ip: host.public_ip ?? '',
      private_ip: host.private_ip ?? '',
      ssh_host: host.ssh_host ?? '',
      ssh_port: host.ssh_port || 22,
      ssh_user: host.ssh_user ?? 'root',
      ssh_password: '',
      ssh_private_key: '',
      ssh_host_key_fingerprint: rescanFingerprint.value,
      clear_ssh_password: false,
      clear_ssh_private_key: false,
      clear_ssh_host_key_fingerprint: false,
      tags: [...host.tags],
    })
    rescanTarget.value = null
  } catch (err) {
    rescanError.value = err instanceof Error ? err.message : String(err)
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
              <span
                v-if="host.ssh_host && !host.ssh_host_key_fingerprint_configured"
                class="fingerprint-warning"
                data-test="host-fingerprint-missing"
              >
                {{ t('settings.hosts.fingerprintMissing') }}
              </span>
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
              <button
                v-if="host.ssh_host_key_fingerprint_configured"
                class="settings-btn settings-btn-text"
                data-test="host-rescan"
                @click="openRescan(host)"
              >
                {{ t('settings.hostForm.rescanTitle') }}
              </button>
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

    <div v-if="rescanTarget" class="settings-modal-backdrop" @click.self="rescanTarget = null">
      <div class="settings-modal">
        <div class="settings-modal-header">
          <h2 class="settings-modal-title">{{ t('settings.hostForm.rescanTitle') }}</h2>
        </div>
        <div class="settings-modal-body">
          <div class="settings-alert settings-alert-danger">{{ t('settings.hostForm.rescanWarning') }}</div>
          <div v-if="rescanError" class="settings-alert settings-alert-danger" data-test="host-rescan-error">
            {{ rescanError }}
          </div>
          <template v-else-if="rescanFingerprint">
            <p>{{ t('settings.hostForm.rescanNewFingerprint') }}</p>
            <code class="mono" data-test="host-rescan-new-fingerprint">{{ rescanFingerprint }}</code>
          </template>
        </div>
        <div class="settings-modal-footer">
          <button type="button" class="settings-btn" @click="rescanTarget = null">{{ t('common.cancel') }}</button>
          <button
            type="button"
            class="settings-btn settings-btn-danger"
            :disabled="!rescanFingerprint"
            data-test="host-rescan-confirm"
            @click="confirmRescan"
          >
            {{ t('settings.hostForm.rescanConfirm') }}
          </button>
        </div>
      </div>
    </div>
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
.fingerprint-warning {
  display: inline-block;
  margin-top: 4px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--status-failed);
  color: #fff;
  font-size: 11px;
}
</style>
