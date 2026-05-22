<!--
HostFormModal：单 Host 新建与编辑表单。

职责：
  - 收集 Host 的 SSH、远端 agent 端口和 tag 字段
  - 提供 ssh_key_path 的本地文件选择入口
  - 将表单 payload 交由父组件保存

边界：
  - 不直接调用远程 API
  - 不负责 SSH config 批量导入
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { open as openDialog } from '@tauri-apps/plugin-dialog'
import type { Host, HostCreatePayload } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  initial?: Host | null
}>()

const emit = defineEmits<{
  submit: [payload: HostCreatePayload]
  cancel: []
}>()

const form = ref<HostCreatePayload>(emptyForm())
const tagsText = ref('')

function emptyForm(): HostCreatePayload {
  return {
    name: '',
    ssh_host: '',
    ssh_port: 22,
    ssh_user: '',
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
      tagsText.value = initial.tags.join(',')
      return
    }
    form.value = emptyForm()
    tagsText.value = ''
  },
  { immediate: true },
)

async function browseKey() {
  const selected = await openDialog({
    multiple: false,
    title: '选择 SSH 私钥文件',
  })
  if (selected && !Array.isArray(selected)) {
    form.value.ssh_key_path = selected
  }
}

function submit() {
  emit('submit', {
    ...form.value,
    tags: tagsText.value.split(',').map(tag => tag.trim()).filter(Boolean),
  })
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑主机' : '新建主机' }}</div>
      <div class="field">
        <label>name <span class="req">*</span></label>
        <input v-model="form.name" placeholder="nova-api-prod-01" data-test="host-form-name" />
      </div>

      <div class="row">
        <div class="field flex">
          <label>ssh_host <span class="req">*</span></label>
          <input v-model="form.ssh_host" placeholder="10.0.0.1" data-test="host-form-host" />
        </div>
        <div class="field port">
          <label>port</label>
          <input v-model.number="form.ssh_port" type="number" min="1" data-test="host-form-port" />
        </div>
      </div>

      <div class="field">
        <label>ssh_user <span class="req">*</span></label>
        <input v-model="form.ssh_user" placeholder="root" data-test="host-form-user" />
      </div>

      <div class="field">
        <label>ssh_password</label>
        <input v-model="form.ssh_password" type="password" placeholder="留空则用密钥" data-test="host-form-password" />
      </div>

      <div class="field">
        <label>ssh_key_path</label>
        <div class="row tight">
          <input v-model="form.ssh_key_path" placeholder="~/.ssh/id_ed25519" data-test="host-form-key" />
          <button type="button" @click="browseKey" data-test="host-form-browse">浏览</button>
        </div>
      </div>

      <div class="field">
        <label>remote_agent_port</label>
        <input v-model.number="form.remote_agent_port" type="number" min="1" data-test="host-form-agent-port" />
      </div>

      <div class="field">
        <label>tags</label>
        <input v-model="tagsText" placeholder="prod,temp" data-test="host-form-tags" />
      </div>

      <div class="warn">密码会以明文存储在本机 hosts 配置文件中，请优先使用密钥。</div>

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
}
.field label {
  margin-bottom: 4px;
  color: var(--text-secondary);
  font-size: 11px;
}
.req,
.warn {
  color: var(--status-failed);
}
.field input {
  padding: 5px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
.row {
  display: flex;
  gap: 8px;
}
.row.tight {
  gap: 4px;
}
.row.tight input,
.field.flex {
  flex: 1;
}
.field.port {
  width: 86px;
}
.warn {
  margin: 12px 0;
  font-size: 11px;
}
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
</style>
