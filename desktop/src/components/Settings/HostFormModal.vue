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
            <label class="settings-field-label">{{ t('settings.hostForm.sshAddress') }} <span class="req">*</span></label>
            <input v-model="form.ssh_host" class="settings-input" placeholder="10.0.0.1" data-test="host-form-host" />
          </div>
          <div class="settings-field port">
            <label class="settings-field-label">{{ t('settings.hostForm.port') }}</label>
            <input v-model.number="form.ssh_port" class="settings-input" type="number" min="1" data-test="host-form-port" />
          </div>
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
          <label class="settings-field-label">{{ t('settings.hostForm.sshUser') }} <span class="req">*</span></label>
          <input v-model="form.ssh_user" class="settings-input" placeholder="root" data-test="host-form-user" />
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshPassword') }}</label>
          <input
            v-model="form.ssh_password"
            class="settings-input"
            type="password"
            :placeholder="t('settings.hostForm.passwordHint')"
            data-test="host-form-password"
          />
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.sshKeyPath') }}</label>
          <div class="row tight">
            <input
              v-model="form.ssh_key_path"
              class="settings-input"
              :placeholder="form.ssh_private_key ? t('settings.hostForm.keyStoredPlaceholder') : '~/.ssh/id_ed25519'"
              data-test="host-form-key"
            />
            <button type="button" class="settings-btn settings-btn-secondary" data-test="host-form-browse" @click="browseKey">
              {{ t('common.browse') }}
            </button>
            <button type="button" class="settings-btn settings-btn-secondary" data-test="host-form-detect" @click="detectKeys">
              {{ t('common.detect') }}
            </button>
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

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hostForm.remoteAgentPort') }}</label>
          <input
            v-model.number="form.remote_agent_port"
            class="settings-input"
            type="number"
            min="1"
            data-test="host-form-agent-port"
          />
        </div>

        <div class="settings-field">
          <label class="settings-field-label">{{ t('settings.hosts.tags') }}</label>
          <TagInput v-model="form.tags!" data-test="host-form-tags" />
        </div>

        <div class="settings-alert settings-alert-warning warn">{{ t('settings.hostForm.warning') }}</div>

        <div class="test-conn">
          <button type="button" class="settings-btn settings-btn-secondary" :disabled="testing" data-test="host-form-test" @click="testConn">
            {{ testing ? t('common.testing') : t('common.testConnection') }}
          </button>
          <span v-if="testResult" :class="testResult.ok ? 'ok' : 'fail'" class="test-msg">
            {{ testResult.ok
              ? t('settings.hostForm.connectSuccess', { latency: testResult.latency_ms })
              : testResult.message }}
          </span>
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
.host-form-body .settings-field {
  position: relative;
}
.req { color: var(--status-failed); }
.row { display: flex; gap: 8px; }
.row.tight { gap: 4px; }
.row.tight .settings-input, .settings-field.flex { flex: 1; }
.settings-field.port { width: 86px; }
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
  margin: 2px 0 0;
}
.test-conn {
  display: flex;
  align-items: center;
  gap: 8px;
}
.test-msg {
  font-size: 11px;
  word-break: break-all;
}
.test-msg.ok { color: var(--status-ok, #3fb950); }
.test-msg.fail { color: var(--status-failed); }
</style>
