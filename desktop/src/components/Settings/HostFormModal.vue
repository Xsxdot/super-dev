<!--
HostFormModal：单 Host 身份信息新建与编辑表单。

职责：
  - 收集 Host 展示名、入口地址元数据、SSH 登录信息和 tag 字段
  - 在 Host 尚无 host key 指纹时，保存前自动采集并要求用户显式确认
  - 将 Host payload 交由父组件保存

边界：
  - 不收集 Agent 端口或 transport chain
  - 不做信任决策：采集只呈现事实，是否信任由用户点击确认
  - 不处理指纹变更后的重采（由 HostManagerTab 的重采流程负责）
  - 不负责 Agent 安装或连接测试
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Host, HostCreatePayload } from '@/api/agent'
import { useRemoteStore } from '@/stores/remote'
import TagInput from './TagInput.vue'

const props = defineProps<{
  visible: boolean
  initial?: Host | null
  error?: string | null
}>()

const emit = defineEmits<{
  submit: [payload: HostCreatePayload]
  cancel: []
}>()

const form = ref<HostCreatePayload>(emptyForm())
const { t } = useAppI18n()

const store = useRemoteStore() // 仅用于只读采集；落库仍由父组件经 emit('submit') 完成

type ScanPhase = 'idle' | 'scanning' | 'confirm' | 'failed'
const scanPhase = ref<ScanPhase>('idle')
const scannedFingerprint = ref('')
const scanErrorMessage = ref('')
const manualEntryOpen = ref(false)

// 已配置指纹的 Host 不再自动采集：换指纹必须走「主机已重装」的重采流程，
// 避免把唯一的安全确认点稀释成一次普通保存。
const needsScan = computed(() => {
  if (props.initial?.ssh_host_key_fingerprint_configured) return false
  return !form.value.ssh_host_key_fingerprint?.trim()
})

const tunnelInvalidationPending = computed(() => {
  const initial = props.initial
  if (!initial) return false
  return form.value.ssh_host !== (initial.ssh_host ?? '')
    || (Number(form.value.ssh_port) || 22) !== (initial.ssh_port || 22)
    || form.value.ssh_user !== (initial.ssh_user ?? 'root')
    || Boolean(form.value.ssh_password?.trim())
    || Boolean(form.value.ssh_private_key?.trim())
    || Boolean(form.value.ssh_host_key_fingerprint?.trim())
    || form.value.clear_ssh_password === true
    || form.value.clear_ssh_private_key === true
})

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

function buildPayload(fingerprint: string): HostCreatePayload {
  // clear 与 replacement 是后端互斥的两个意图；disabled 只影响交互，不会清空 Vue model。
  // 在唯一提交边界归一化，才能保证用户先输入再勾选清除时不会产生冲突请求。
  // clear_ssh_host_key_fingerprint 恒为 false：清除指纹改走 Task 7 的显式重采流程，
  // 但字段仍保留在 payload 类型与后端契约中，此处不删除。
  return {
    name: form.value.name,
    public_ip: form.value.public_ip,
    private_ip: form.value.private_ip,
    ssh_host: form.value.ssh_host,
    ssh_port: Number(form.value.ssh_port) || 22,
    ssh_user: form.value.ssh_user,
    ssh_password: form.value.clear_ssh_password ? '' : form.value.ssh_password,
    ssh_private_key: form.value.clear_ssh_private_key ? '' : form.value.ssh_private_key,
    ssh_host_key_fingerprint: fingerprint,
    clear_ssh_password: form.value.clear_ssh_password,
    clear_ssh_private_key: form.value.clear_ssh_private_key,
    clear_ssh_host_key_fingerprint: false,
    tags: form.value.tags ?? [],
  }
}

async function submit() {
  // 手填或已有指纹时直接提交；否则先采集再由用户确认。
  if (!needsScan.value) {
    emit('submit', buildPayload(form.value.ssh_host_key_fingerprint ?? ''))
    return
  }
  await runScan()
}

async function runScan() {
  scanPhase.value = 'scanning'
  scanErrorMessage.value = ''
  try {
    const result = await store.scanHostKey({
      ssh_host: form.value.ssh_host,
      ssh_port: Number(form.value.ssh_port) || 22,
    })
    scannedFingerprint.value = result.fingerprint
    scanPhase.value = 'confirm'
  } catch (err) {
    // 采集失败绝不降级为「跳过指纹直接保存」——必须由用户显式选择后续动作，
    // 否则「未采集」与「采集出错」会被混淆，用户会以为已经配好。
    scanErrorMessage.value = err instanceof Error ? err.message : String(err)
    scanPhase.value = 'failed'
  }
}

function trustAndSave() {
  emit('submit', buildPayload(scannedFingerprint.value))
  scanPhase.value = 'idle'
}

function cancelScan() {
  scanPhase.value = 'idle'
  scannedFingerprint.value = ''
}

function openManualEntry() {
  manualEntryOpen.value = true
  scanPhase.value = 'idle'
}

function saveWithoutFingerprint() {
  emit('submit', buildPayload(''))
  scanPhase.value = 'idle'
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" @click.self="emit('cancel')">
    <div class="settings-modal">
      <div class="settings-modal-header">
        <h2 class="settings-modal-title">{{ initial ? t('settings.hosts.edit') : t('settings.hosts.add') }}</h2>
      </div>

      <div class="settings-modal-body host-form-body">
        <div v-if="error" class="settings-alert settings-alert-danger" data-test="host-form-error">
          {{ error }}
        </div>
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
          <button
            v-if="!manualEntryOpen"
            type="button"
            class="settings-btn settings-btn-text"
            data-test="host-form-manual-fingerprint-toggle"
            @click="manualEntryOpen = true"
          >
            {{ t('settings.hostForm.manualFingerprintToggle') }}
          </button>
          <template v-else>
            <label class="settings-field-label">{{ t('settings.hostForm.hostKeyFingerprint') }}</label>
            <input
              v-model="form.ssh_host_key_fingerprint"
              class="settings-input mono-input"
              placeholder="SHA256:..."
              data-test="host-form-ssh-host-key-fingerprint"
            />
            <span class="settings-field-hint">{{ t('settings.hostForm.hostKeyFingerprintHint') }}</span>
          </template>
        </div>

        <div v-if="scanPhase === 'scanning'" class="settings-alert" data-test="host-form-scanning">
          {{ t('settings.hostForm.scanning') }}
        </div>

        <div v-if="scanPhase === 'confirm'" class="settings-alert settings-alert-warning" data-test="host-form-scan-confirm">
          <strong>{{ t('settings.hostForm.scanConfirmTitle') }}</strong>
          <p>{{ t('settings.hostForm.scanConfirmBody', { addr: `${form.ssh_host}:${form.ssh_port}` }) }}</p>
          <code class="mono-input" data-test="host-form-scan-fingerprint">{{ scannedFingerprint }}</code>
          <div class="scan-actions">
            <button type="button" class="settings-btn" data-test="host-form-scan-cancel" @click="cancelScan">
              {{ t('common.cancel') }}
            </button>
            <button type="button" class="settings-btn settings-btn-primary" data-test="host-form-scan-trust" @click="trustAndSave">
              {{ t('settings.hostForm.scanTrustAndSave') }}
            </button>
          </div>
        </div>

        <div v-if="scanPhase === 'failed'" class="settings-alert settings-alert-danger" data-test="host-form-scan-failed">
          <strong>{{ t('settings.hostForm.scanFailed') }}</strong>
          <p>{{ scanErrorMessage }}</p>
          <div class="scan-actions">
            <button type="button" class="settings-btn" data-test="host-form-scan-retry" @click="runScan">
              {{ t('settings.hostForm.scanRetry') }}
            </button>
            <button type="button" class="settings-btn" data-test="host-form-scan-manual" @click="openManualEntry">
              {{ t('settings.hostForm.scanManualEntry') }}
            </button>
            <button type="button" class="settings-btn settings-btn-text" data-test="host-form-save-without-fingerprint" @click="saveWithoutFingerprint">
              {{ t('settings.hostForm.saveWithoutFingerprint') }}
            </button>
          </div>
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

        <div v-if="tunnelInvalidationPending" class="settings-alert settings-alert-warning" data-test="host-form-tunnel-invalidation">
          {{ t('settings.hostForm.tunnelInvalidationWarning') }}
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
.scan-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
  flex-wrap: wrap;
}
</style>
