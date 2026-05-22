<!--
HostFormModal：单 Host 新建与编辑表单。

职责：
  - 收集 Host 的 SSH、远端 agent 端口和 tag 字段
  - ssh_user 新建时默认 root
  - ssh_key_path 提供浏览和自动检测两种入口
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

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_password: '',
    ssh_key_path: '',
    remote_agent_port: 57017,
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
        remote_agent_port: initial.remote_agent_port,
        tags: [...initial.tags],
      }
      return
    }
    form.value = emptyForm()
  },
  { immediate: true },
)

async function browseKey() {
  const selected = await openDialog({ multiple: false, title: '选择 SSH 私钥文件' })
  if (selected && !Array.isArray(selected)) {
    form.value.ssh_key_path = selected
  }
}

async function detectKeys() {
  try {
    keyOptions.value = await api.detectSshKeys()
    showKeyDropdown.value = keyOptions.value.length > 0
    if (keyOptions.value.length === 0) {
      testResult.value = { ok: false, message: '未在 ~/.ssh/ 找到私钥文件' }
    }
  } catch (err) {
    testResult.value = { ok: false, message: err instanceof Error ? err.message : '检测失败' }
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
    })
    testResult.value = result
  } catch (err) {
    testResult.value = { ok: false, message: err instanceof Error ? err.message : '请求失败' }
  } finally {
    testing.value = false
  }
}

function submit() {
  emit('submit', { ...form.value })
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑主机' : '新建主机' }}</div>

      <div class="field">
        <label>名称 <span class="req">*</span></label>
        <input v-model="form.name" placeholder="nova-api-prod-01" data-test="host-form-name" />
      </div>

      <div class="row">
        <div class="field flex">
          <label>SSH 地址 <span class="req">*</span></label>
          <input v-model="form.ssh_host" placeholder="10.0.0.1" data-test="host-form-host" />
        </div>
        <div class="field port">
          <label>端口</label>
          <input v-model.number="form.ssh_port" type="number" min="1" data-test="host-form-port" />
        </div>
      </div>

      <div class="field">
        <label>SSH 用户 <span class="req">*</span></label>
        <input v-model="form.ssh_user" placeholder="root" data-test="host-form-user" />
      </div>

      <div class="field">
        <label>SSH 密码</label>
        <input v-model="form.ssh_password" type="password" placeholder="留空则用密钥" data-test="host-form-password" />
      </div>

      <div class="field">
        <label>SSH 私钥路径</label>
        <div class="row tight">
          <input v-model="form.ssh_key_path" placeholder="~/.ssh/id_ed25519" data-test="host-form-key" />
          <button type="button" @click="browseKey" data-test="host-form-browse">浏览</button>
          <button type="button" @click="detectKeys" data-test="host-form-detect">检测</button>
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
        <label>远端 Agent 端口</label>
        <input v-model.number="form.remote_agent_port" type="number" min="1" data-test="host-form-agent-port" />
      </div>

      <div class="field">
        <label>标签</label>
        <TagInput v-model="form.tags!" data-test="host-form-tags" />
      </div>

      <div class="warn">密码会以明文存储在本机 hosts 配置文件中，请优先使用密钥。</div>

      <div class="test-conn">
        <button type="button" :disabled="testing" data-test="host-form-test" @click="testConn">
          {{ testing ? '测试中…' : '测试连接' }}
        </button>
        <span v-if="testResult" :class="testResult.ok ? 'ok' : 'fail'" class="test-msg">
          {{ testResult.ok
            ? `连接成功（${testResult.latency_ms}ms）`
            : testResult.message }}
        </span>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button type="button" class="primary" @click="submit" data-test="host-form-submit">保存</button>
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
