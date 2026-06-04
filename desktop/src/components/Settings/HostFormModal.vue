<!--
HostFormModal：单 Host 新建与编辑表单。

职责：
  - 收集 Host 的 SSH、入口地址元数据、远端 agent 端口和 tag 字段
  - ssh_user 新建时默认 root
  - ssh_key_path 提供浏览和自动检测两种私钥导入入口
  - 提供测试连接入口，展示完整错误信息
  - 将表单 payload 交由父组件保存

边界：
  - 不直接调用远程 API（测试连接和密钥检测除外）
  - 不负责 SSH config 批量导入
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { open as openDialog } from '@tauri-apps/plugin-dialog'
import { api } from '@/api/agent'
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
const keyOptions = ref<string[]>([])
const showKeyDropdown = ref(false)
const testResult = ref<{ ok: boolean; message: string; latency_ms?: number } | null>(null)
const testing = ref(false)
const { t } = useAppI18n()

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_password: '',
    ssh_key_path: '',
    ssh_private_key: '',
    remote_agent_port: 57017,
    public_ip: '',
    private_ip: '',
    tags: [],
  }
}

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    testResult.value = null
    keyOptions.value = []
    showKeyDropdown.value = false
    if (initial) {
      form.value = {
        name: initial.name,
        ssh_host: initial.ssh_host,
        ssh_port: initial.ssh_port,
        ssh_user: initial.ssh_user,
        ssh_password: initial.ssh_password ?? '',
        ssh_key_path: initial.ssh_key_path ?? '',
        ssh_private_key: initial.ssh_private_key ?? '',
        remote_agent_port: initial.remote_agent_port,
        public_ip: initial.public_ip ?? '',
        private_ip: initial.private_ip ?? '',
        tags: [...initial.tags],
      }
      return
    }
    form.value = emptyForm()
  },
  { immediate: true },
)

async function browseKey() {
  const selected = await openDialog({ multiple: false, title: t('settings.hostForm.keyFileTitle') })
  if (selected && !Array.isArray(selected)) {
    form.value.ssh_key_path = selected
  }
}

async function detectKeys() {
  try {
    keyOptions.value = await api.detectSshKeys()
    showKeyDropdown.value = keyOptions.value.length > 0
    if (keyOptions.value.length === 0) {
      testResult.value = { ok: false, message: t('settings.hostForm.noPrivateKey') }
    }
  } catch (err) {
    testResult.value = { ok: false, message: err instanceof Error ? err.message : t('common.detectFailed') }
  }
}

function selectKey(path: string) {
  form.value.ssh_key_path = path
  showKeyDropdown.value = false
}

async function testConn() {
  testing.value = true
  testResult.value = null
  try {
    const result = await api.testConnection({
      ssh_host: form.value.ssh_host,
      ssh_port: form.value.ssh_port ?? 22,
      ssh_user: form.value.ssh_user,
      ssh_password: form.value.ssh_password,
      ssh_key_path: form.value.ssh_key_path,
      ssh_private_key: form.value.ssh_private_key,
    })
    testResult.value = result
  } catch (err) {
    testResult.value = { ok: false, message: err instanceof Error ? err.message : t('common.requestFailed') }
  } finally {
    testing.value = false
  }
}

function submit() {
  const payload = { ...form.value }
  if (payload.ssh_key_path?.trim()) {
    // 路径只作为导入源；保存时由后端读取文件内容并清空持久化路径。
    payload.ssh_private_key = ''
  }
  emit('submit', payload)
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? t('settings.hosts.edit') : t('settings.hosts.add') }}</div>

      <div class="field">
        <label>{{ t('settings.hosts.name') }} <span class="req">*</span></label>
        <input v-model="form.name" placeholder="nova-api-prod-01" data-test="host-form-name" />
      </div>

      <div class="row">
        <div class="field flex">
          <label>{{ t('settings.hostForm.sshAddress') }} <span class="req">*</span></label>
          <input v-model="form.ssh_host" placeholder="10.0.0.1" data-test="host-form-host" />
        </div>
        <div class="field port">
          <label>{{ t('settings.hostForm.port') }}</label>
          <input v-model.number="form.ssh_port" type="number" min="1" data-test="host-form-port" />
        </div>
      </div>

      <div class="row">
        <div class="field flex">
          <label>{{ t('settings.hostForm.publicIP') }}</label>
          <input v-model="form.public_ip" placeholder="203.0.113.10" data-test="host-form-public-ip" />
        </div>
        <div class="field flex">
          <label>{{ t('settings.hostForm.privateIP') }}</label>
          <input v-model="form.private_ip" placeholder="10.0.0.10" data-test="host-form-private-ip" />
        </div>
      </div>

      <div class="field">
        <label>{{ t('settings.hostForm.sshUser') }} <span class="req">*</span></label>
        <input v-model="form.ssh_user" placeholder="root" data-test="host-form-user" />
      </div>

      <div class="field">
        <label>{{ t('settings.hostForm.sshPassword') }}</label>
        <input v-model="form.ssh_password" type="password" :placeholder="t('settings.hostForm.passwordHint')" data-test="host-form-password" />
      </div>

      <div class="field">
        <label>{{ t('settings.hostForm.sshKeyPath') }}</label>
        <div class="row tight">
          <input
            v-model="form.ssh_key_path"
            :placeholder="form.ssh_private_key ? t('settings.hostForm.keyStoredPlaceholder') : '~/.ssh/id_ed25519'"
            data-test="host-form-key"
          />
          <button type="button" @click="browseKey" data-test="host-form-browse">{{ t('common.browse') }}</button>
          <button type="button" @click="detectKeys" data-test="host-form-detect">{{ t('common.detect') }}</button>
        </div>
        <div v-if="form.ssh_private_key && !form.ssh_key_path" class="hint" data-test="host-form-key-stored">
          {{ t('settings.hostForm.keyStoredHint') }}
        </div>
        <div v-if="showKeyDropdown" class="key-dropdown">
          <div
            v-for="k in keyOptions"
            :key="k"
            class="key-option"
            @click="selectKey(k)"
          >{{ k }}</div>
        </div>
      </div>

      <div class="field">
        <label>{{ t('settings.hostForm.remoteAgentPort') }}</label>
        <input v-model.number="form.remote_agent_port" type="number" min="1" data-test="host-form-agent-port" />
      </div>

      <div class="field">
        <label>{{ t('settings.hosts.tags') }}</label>
        <TagInput v-model="form.tags!" data-test="host-form-tags" />
      </div>

      <div class="warn">{{ t('settings.hostForm.warning') }}</div>

      <div class="test-conn">
        <button type="button" :disabled="testing" data-test="host-form-test" @click="testConn">
          {{ testing ? t('common.testing') : t('common.testConnection') }}
        </button>
        <span v-if="testResult" :class="testResult.ok ? 'ok' : 'fail'" class="test-msg">
          {{ testResult.ok
            ? t('settings.hostForm.connectSuccess', { latency: testResult.latency_ms })
            : testResult.message }}
        </span>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">{{ t('common.cancel') }}</button>
        <button type="button" class="primary" @click="submit" data-test="host-form-submit">{{ t('common.save') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
}
.modal-body {
  width: min(480px, calc(100vw - 32px));
  max-height: 86vh;
  overflow-y: auto;
  padding: 16px 18px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.modal-title {
  margin-bottom: 12px;
  font-size: 14px;
  font-weight: 600;
}
.field {
  display: flex;
  flex-direction: column;
  margin-bottom: 10px;
  position: relative;
}
.field label {
  margin-bottom: 4px;
  color: var(--text-secondary);
  font-size: 11px;
}
.req { color: var(--status-failed); }
.field input {
  padding: 5px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
.row { display: flex; gap: 8px; }
.row.tight { gap: 4px; }
.row.tight input, .field.flex { flex: 1; }
.field.port { width: 86px; }
.key-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  z-index: 10;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
  max-height: 140px;
  overflow-y: auto;
}
.key-option {
  padding: 5px 8px;
  font-size: 11px;
  font-family: var(--font-mono, monospace);
  cursor: pointer;
}
.key-option:hover { background: var(--bg-secondary); }
.hint {
  margin-top: 4px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.warn {
  margin: 12px 0 8px;
  color: var(--status-failed);
  font-size: 11px;
}
.test-conn {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.test-msg {
  font-size: 11px;
  word-break: break-all;
}
.test-msg.ok { color: var(--status-ok, #3fb950); }
.test-msg.fail { color: var(--status-failed); }
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
button {
  padding: 5px 12px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  cursor: pointer;
  font-size: 12px;
}
button.primary {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
button:disabled { cursor: not-allowed; opacity: 0.5; }
</style>
