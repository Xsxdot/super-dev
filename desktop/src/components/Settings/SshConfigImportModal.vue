<!--
SshConfigImportModal：列出 ~/.ssh/config 中的 Host 条目供多选导入。

职责：
  - 打开时读取 SSH config Host 条目
  - 维护当前弹窗内的多选状态
  - 将选中的 entries 交给父组件批量创建 Host

边界：
  - 不与本地 SSH config 保持同步
  - 不补全 tags，由用户后续编辑 Host
-->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type SshConfigEntry } from '@/api/agent'

const props = defineProps<{ visible: boolean }>()

const emit = defineEmits<{
  import: [entries: SshConfigEntry[]]
  cancel: []
}>()

const loading = ref(false)
const error = ref<string | null>(null)
const entries = ref<SshConfigEntry[]>([])
const selected = ref<Set<string>>(new Set())

watch(
  () => props.visible,
  async visible => {
    if (!visible) return
    loading.value = true
    error.value = null
    selected.value = new Set()
    try {
      entries.value = await api.listSshConfigHosts()
    } catch (err) {
      error.value = err instanceof Error ? err.message : '读取 SSH config 失败'
      entries.value = []
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)

function toggle(host: string) {
  const next = new Set(selected.value)
  if (next.has(host)) next.delete(host)
  else next.add(host)
  selected.value = next
}

function confirmImport() {
  emit('import', entries.value.filter(entry => selected.value.has(entry.host)))
}
</script>

<template>
  <div v-if="visible" class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal-body">
      <div class="modal-title">从 SSH config 导入</div>
      <div v-if="loading" class="state">读取中...</div>
      <div v-else-if="error" class="state err">{{ error }}</div>
      <div v-else-if="entries.length === 0" class="state">没有可导入的 Host</div>
      <ul v-else class="entry-list">
        <li
          v-for="entry in entries"
          :key="entry.host"
          :class="{ selected: selected.has(entry.host) }"
          data-test="ssh-import-row"
          @click="toggle(entry.host)"
        >
          <input type="checkbox" :checked="selected.has(entry.host)" @click.stop="toggle(entry.host)" />
          <span class="name">{{ entry.host }}</span>
          <span class="meta">{{ entry.user }}@{{ entry.hostname }}:{{ entry.port }}</span>
          <span v-if="entry.identity_file" class="meta key">{{ entry.identity_file }}</span>
        </li>
      </ul>
      <div class="actions">
        <button type="button" @click="emit('cancel')">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="selected.size === 0"
          data-test="ssh-import-confirm"
          @click="confirmImport"
        >
          导入 {{ selected.size }} 项
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
  width: min(520px, calc(100vw - 32px));
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
.state {
  padding: 16px;
  color: var(--text-tertiary);
  text-align: center;
  font-size: 12px;
}
.state.err {
  color: var(--status-failed);
}
.entry-list {
  max-height: 360px;
  padding: 0;
  margin: 0 0 12px;
  overflow-y: auto;
  list-style: none;
}
.entry-list li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border-secondary);
  cursor: pointer;
}
.entry-list li.selected {
  background: var(--bg-secondary);
}
.name {
  font-weight: 600;
  font-size: 12px;
}
.meta {
  color: var(--text-tertiary);
  font-size: 11px;
}
.key {
  margin-left: 4px;
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
