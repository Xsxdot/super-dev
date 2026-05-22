<!--
LogSourceFormModal：远程监听任务新建与编辑表单。

职责：
  - 收集 LogSource 的 name、type 和关联 host_ids
  - 根据远程 Host 列表渲染多选项
  - 将 payload 交由父组件保存

边界：
  - 不直接调用 API
  - 不负责 Host 新建，缺少 Host 时只提示前往设置页
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import type { LogSource, LogSourceCreatePayload, LogSourceType } from '@/api/agent'

const props = defineProps<{
  visible: boolean
  initial?: LogSource | null
}>()

const emit = defineEmits<{
  submit: [payload: LogSourceCreatePayload]
  cancel: []
}>()

const store = useRemoteStore()
const name = ref('')
const type = ref<LogSourceType>('journalctl')
const hostIds = ref<Set<string>>(new Set())

watch(
  () => [props.visible, props.initial] as const,
  ([visible, initial]) => {
    if (!visible) return
    if (initial) {
      name.value = initial.name
      type.value = initial.type
      hostIds.value = new Set(initial.host_ids)
      return
    }
    name.value = ''
    type.value = 'journalctl'
    hostIds.value = new Set()
  },
  { immediate: true },
)

const canSubmit = computed(() => name.value.trim().length > 0 && hostIds.value.size > 0)

function toggleHost(id: string) {
  const next = new Set(hostIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  hostIds.value = next
}

function submit() {
  emit('submit', {
    name: name.value.trim(),
    type: type.value,
    host_ids: Array.from(hostIds.value),
  })
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">{{ initial ? '编辑监听任务' : '新建监听任务' }}</div>

      <div class="field">
        <label>name <span class="req">*</span></label>
        <input v-model="name" placeholder="nova-api" data-test="logsource-form-name" />
      </div>

      <div class="field">
        <label>type</label>
        <select v-model="type" data-test="logsource-form-type">
          <option value="journalctl">journalctl</option>
          <option value="docker">docker</option>
        </select>
      </div>

      <div class="field">
        <label>关联主机 <span class="req">*</span></label>
        <div v-if="store.hosts.length > 0" class="host-list">
          <label v-for="host in store.hosts" :key="host.id" class="host-row" data-test="logsource-form-host">
            <input type="checkbox" :checked="hostIds.has(host.id)" @change="toggleHost(host.id)" />
            <span class="hname">{{ host.name }}</span>
            <span class="tags">{{ host.tags.join(', ') || '(无标签)' }}</span>
          </label>
        </div>
        <div v-else class="empty">还没有主机。请先到设置页添加。</div>
      </div>

      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="!canSubmit"
          data-test="logsource-form-submit"
          @click="submit"
        >
          保存
        </button>
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
  width: min(440px, calc(100vw - 32px));
  max-height: 80vh;
  overflow-y: auto;
  padding: 16px 18px;
  background: var(--bg-primary);
  border: 1px solid var(--border-secondary);
}
.modal-title {
  margin-bottom: 10px;
  font-size: 14px;
  font-weight: 600;
}
.field {
  display: flex;
  flex-direction: column;
  margin-bottom: 12px;
}
.field label {
  margin-bottom: 4px;
  color: var(--text-secondary);
  font-size: 11px;
}
.req {
  color: var(--status-failed);
}
.field input,
.field select {
  padding: 5px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  font-size: 12px;
}
.host-list {
  max-height: 240px;
  overflow-y: auto;
  border: 1px solid var(--border-secondary);
}
.host-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 8px;
  cursor: pointer;
  font-size: 12px;
}
.host-row:hover {
  background: var(--bg-secondary);
}
.hname {
  font-weight: 600;
}
.tags,
.empty {
  color: var(--text-tertiary);
  font-size: 11px;
}
.empty {
  padding: 12px;
  text-align: center;
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
button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
