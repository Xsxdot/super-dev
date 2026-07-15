<!--
HostFormModal：单 Host 身份信息新建与编辑表单。

职责：
  - 收集 Host 展示名、入口地址元数据、SSH 登录信息和 tag 字段
  - 将 Host payload 交由父组件保存

边界：
  - 不收集 Agent 端口或 transport chain
  - 不直接调用远程 API
  - 不负责 Agent 安装或连接测试
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Host, HostCreatePayload } from '@/api/agent'
import TagInput from './TagInput.vue'

const props = defineProps<{
  visible: boolean
  initial?: Host | null
}>()

const emit = defineEmits<{
  submit: [payload: HostCreatePayload]
  cancel: []
}>()

const form = ref<HostCreatePayload>(emptyForm())
const { t } = useAppI18n()

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    public_ip: '',
    private_ip: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_password: '',
    ssh_private_key: '',
    ssh_host_key_fingerprint: '',
    clear_ssh_password: false,
    clear_ssh_private_key: false,
    clear_ssh_host_key_fingerprint: false,
    tags: [],
  }
}

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    if (initial) {
      form.value = {
        name: initial.name,
        public_ip: initial.public_ip ?? '',
        private_ip: initial.private_ip ?? '',
        ssh_host: initial.ssh_host ?? '',
        ssh_port: initial.ssh_port || 22,
        ssh_user: initial.ssh_user ?? 'root',
        // Host read view 永不回显秘密或 pin；空值由后端解释为保留既有配置。
        ssh_password: '',
        ssh_private_key: '',
        ssh_host_key_fingerprint: '',
        clear_ssh_password: false,
        clear_ssh_private_key: false,
        clear_ssh_host_key_fingerprint: false,
        tags: [...initial.tags],
      }
      return
    }
    form.value = emptyForm()
  },
  { immediate: true },
)

function submit() {
  emit('submit', {
    name: form.value.name,
    public_ip: form.value.public_ip,
    private_ip: form.value.private_ip,
    ssh_host: form.value.ssh_host,
    ssh_port: Number(form.value.ssh_port) || 22,
    ssh_user: form.value.ssh_user,
    ssh_password: form.value.ssh_password,
    ssh_private_key: form.value.ssh_private_key,
    ssh_host_key_fingerprint: form.value.ssh_host_key_fingerprint,
    clear_ssh_password: form.value.clear_ssh_password,
    clear_ssh_private_key: form.value.clear_ssh_private_key,
    clear_ssh_host_key_fingerprint: form.value.clear_ssh_host_key_fingerprint,
    tags: form.value.tags ?? [],
  })
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <div class="settings-modal">
      <div class="settings-modal-header">
        <h2 class="settings-modal-title">{{ initial ? t('settings.hosts.edit') : t('settings.hosts.add') }}</h2>
      </div>

      <div class="settings-modal-body host-form-body">
        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.name') }} <span class="req">*</span></label>
          <input v-model="form.name" class="settings-input" placeholder="nova-api-prod-01" data-test="host-form-name" />
        </div>

        <div class="row">
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.publicIP') }}</label>
            <input v-model="form.public_ip" class="settings-input" placeholder="203.0.113.10" data-test="host-form-public-ip" />
          </div>
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.privateIP') }}</label>
            <input v-model="form.private_ip" class="settings-input" placeholder="10.0.0.10" data-test="host-form-private-ip" />
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.tags') }}</label>
          <TagInput v-model="form.tags!" data-test="host-form-tags" />
        </div>

        <div class="row">
          <div class="settings-field flex">
            <label class="settings-field-label">{{ t('settings.hostForm.sshAddress') }}</label>
            <input v-model="form.ssh_host" class="settings-input" placeholder="10.0.0.10" data-test="host-form-ssh-host" />
          </div>
          <div class="settings-field port">
            <label class="settings-field-label">{{ t('settings.hostForm.port') }}</label>
            <input v-model.number="form.ssh_port" class="settings-input" type="number" min="1" data-test="host-form-ssh-port" />
          </div>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshUser') }}</label>
          <input v-model="form.ssh_user" class="settings-input" placeholder="root" data-test="host-form-ssh-user" />
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.hostKeyFingerprint') }}</label>
          <input
            v-model="form.ssh_host_key_fingerprint"
            class="settings-input mono-input"
            :disabled="form.clear_ssh_host_key_fingerprint"
            placeholder="SHA256:..."
            data-test="host-form-ssh-host-key-fingerprint"
          />
          <span class="settings-field-hint">{{ t('settings.hostForm.hostKeyFingerprintHint') }}</span>
          <label v-if="initial?.ssh_host_key_fingerprint_configured" class="credential-clear">
            <input v-model="form.clear_ssh_host_key_fingerprint" type="checkbox" data-test="host-form-clear-ssh-host-key-fingerprint" />
            {{ t('settings.hostForm.clearStoredHostKeyFingerprint') }}
          </label>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshPassword') }}</label>
          <input
            v-model="form.ssh_password"
            class="settings-input"
            type="password"
            :disabled="form.clear_ssh_password"
            :placeholder="initial?.ssh_password_configured ? t('settings.hostForm.storedSecretHint') : t('settings.hostForm.passwordHint')"
            data-test="host-form-ssh-password"
          />
          <label v-if="initial?.ssh_password_configured" class="credential-clear">
            <input v-model="form.clear_ssh_password" type="checkbox" data-test="host-form-clear-ssh-password" />
            {{ t('settings.hostForm.clearStoredPassword') }}
          </label>
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshPrivateKey') }}</label>
          <textarea
            v-model="form.ssh_private_key"
            class="settings-input key-box"
            :disabled="form.clear_ssh_private_key"
            :placeholder="initial?.ssh_private_key_configured ? t('settings.hostForm.storedSecretHint') : ''"
            data-test="host-form-ssh-private-key"
          />
          <label v-if="initial?.ssh_private_key_configured" class="credential-clear">
            <input v-model="form.clear_ssh_private_key" type="checkbox" data-test="host-form-clear-ssh-private-key" />
            {{ t('settings.hostForm.clearStoredPrivateKey') }}
          </label>
        </div>
      </div>

      <div class="settings-modal-footer">
        <button type="button" class="settings-btn" data-test="host-form-cancel" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button type="button" class="settings-btn settings-btn-primary" data-test="host-form-submit" @click="submit">{{ t('common.save') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.host-form-body {
  display: grid;
  gap: 10px;
}
.req { color: var(--status-failed); }
.row { display: flex; gap: 8px; }
.settings-field.flex { flex: 1; }
.port { width: 96px; }
.key-box {
  min-height: 112px;
  font-family: var(--font-mono, monospace);
  resize: vertical;
}
.mono-input {
  font-family: var(--font-mono, monospace);
}
.credential-clear {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
