<!--
RemoteListenSection：Sidebar 中的远程监听块。

职责：
  - 展示远程监听标题、主机管理入口和 LogSource 列表
  - 协调监听任务表单的新建、编辑、删除
  - 将分组打开事件上抛给 SidebarView

边界：
  - 不解析分组，委托 remote store
  - 不打开日志面板，只 emit open 事件
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useRemoteStore } from '@/stores/remote'
import RemoteLogSourceRow from './RemoteLogSourceRow.vue'
import LogSourceFormModal from './LogSourceFormModal.vue'
import type { LogSource, LogSourceCreatePayload } from '@/api/agent'

const emit = defineEmits<{
  open: [payload: { logSourceId: string; groupKey: string }]
}>()

const router = useRouter()
const store = useRemoteStore()
const formVisible = ref(false)
const editing = ref<LogSource | null>(null)
const error = ref<string | null>(null)

const unboundLogSources = computed(() =>
  store.logSources.filter(ls => !ls.project_id)
)

onMounted(async () => {
  try {
    await Promise.all([store.loadHosts(), store.loadLogSources(), store.loadTunnels()])
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载失败'
  }
})

function openSettings() {
  router.push({ path: '/settings', query: { tab: 'hosts' } })
}

function openCreate() {
  editing.value = null
  formVisible.value = true
}

function handleEdit(logSource: LogSource) {
  editing.value = logSource
  formVisible.value = true
}

async function handleDelete(logSource: LogSource) {
  if (!confirm(`确认删除监听任务 "${logSource.name}"？`)) return
  try {
    await store.deleteLogSource(logSource.id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : '删除失败'
  }
}

async function handleSubmit(payload: LogSourceCreatePayload) {
  try {
    if (editing.value) {
      await store.updateLogSource(editing.value.id, payload)
    } else {
      await store.createLogSource(payload)
    }
    formVisible.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : '保存失败'
  }
}
</script>

<template>
  <div class="remote-section">
    <div class="section-header">
      <span class="title">远程监听</span>
      <button class="gear" title="主机管理" data-test="remote-gear" @click="openSettings">⚙</button>
    </div>
    <div v-if="error" class="error">{{ error }}</div>
    <RemoteLogSourceRow
      v-for="logSource in unboundLogSources"
      :key="logSource.id"
      :log-source="logSource"
      @open="payload => emit('open', payload)"
      @edit="handleEdit"
      @delete="handleDelete"
    />
    <div v-if="unboundLogSources.length === 0 && !error" class="empty">还没有监听任务</div>
    <div class="add-row" data-test="remote-add-logsource" @click="openCreate">+ 新建监听任务</div>

    <LogSourceFormModal
      :visible="formVisible"
      :initial="editing"
      @submit="handleSubmit"
      @cancel="formVisible = false"
    />
  </div>
</template>

<style scoped>
.remote-section {
  padding: 6px 0;
  border-top: 1px solid var(--border-secondary);
}
.section-header {
  display: flex;
  align-items: center;
  padding: 4px 12px;
}
.title {
  flex: 1;
  color: var(--text-tertiary);
  font-size: 11px;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}
.gear {
  padding: 0 4px;
  color: var(--text-tertiary);
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 13px;
}
.gear:hover {
  color: var(--text-secondary);
}
.error {
  padding: 4px 12px;
  color: var(--status-failed);
  font-size: 11px;
}
.empty,
.add-row {
  padding: 5px 12px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.add-row {
  cursor: pointer;
}
.add-row:hover {
  color: var(--text-secondary);
}
</style>
