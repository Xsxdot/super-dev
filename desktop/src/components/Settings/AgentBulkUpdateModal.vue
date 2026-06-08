<!--
AgentBulkUpdateModal：Agent 批量二进制更新弹窗。

职责：
  - 展示本地打包 Agent 目标版本和候选远端 Agent
  - 根据 Host SSH 与 Agent runtime 判断默认选中、可尝试和禁选状态
  - 以前端并发 worker 调用单台 update-binary，并展示逐台结果

边界：
  - 不编辑 Agent transport/config/security
  - 不重新 provision 安全配置
  - 不持久化批量任务历史
-->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AgentDTO, Host } from '@/api/agent'
import { useAgentsStore } from '@/stores/agents'
import { buildBulkUpdateRows, runAgentUpdateBatch, type AgentBulkUpdateRow } from '@/lib/agentBulkUpdate'

type RunStatus = 'idle' | 'queued' | 'running' | 'success' | 'failed' | 'skipped'

const props = defineProps<{
  visible: boolean
  agents: AgentDTO[]
  hosts: Host[]
}>()
const emit = defineEmits<{ cancel: [] }>()

const { t } = useI18n()
const agentsStore = useAgentsStore()
const targetVersion = ref('')
const concurrency = ref(3)
const loadingTarget = ref(false)
const loadError = ref<string | null>(null)
const running = ref(false)
const selected = ref<Set<string>>(new Set())
const statuses = reactive<Record<string, RunStatus>>({})
const errors = reactive<Record<string, string>>({})

const rows = computed(() => buildBulkUpdateRows(props.agents, props.hosts, targetVersion.value))
const selectedCount = computed(() => rows.value.filter(row => selected.value.has(row.hostId) && !row.disabled).length)
const canStart = computed(() => selectedCount.value > 0 && !running.value && !loadingTarget.value)

watch(() => props.visible, async visible => {
  if (!visible) return
  await loadTarget()
}, { immediate: true })

watch(rows, nextRows => {
  if (running.value) return
  const next = new Set<string>()
  for (const row of nextRows) {
    statuses[row.hostId] = 'idle'
    errors[row.hostId] = ''
    if (row.selected && !row.disabled) next.add(row.hostId)
  }
  selected.value = next
}, { immediate: true })

async function loadTarget() {
  loadingTarget.value = true
  loadError.value = null
  try {
    const target = await agentsStore.getAgentUpdateTarget()
    targetVersion.value = target.version
    concurrency.value = target.concurrency_default || 3
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : t('settings.agents.bulkUpdateLoadFailed')
  } finally {
    loadingTarget.value = false
  }
}

function toggle(row: AgentBulkUpdateRow, checked: boolean) {
  if (running.value || row.disabled) return
  const next = new Set(selected.value)
  if (checked) next.add(row.hostId)
  else next.delete(row.hostId)
  selected.value = next
}

function selectDefault() {
  if (running.value) return
  selected.value = new Set(rows.value.filter(row => row.eligibility === 'selected-by-default').map(row => row.hostId))
}

function clearSelection() {
  if (running.value) return
  selected.value = new Set()
}

async function start() {
  if (!canStart.value) return
  running.value = true
  const hostIds = rows.value.filter(row => selected.value.has(row.hostId) && !row.disabled).map(row => row.hostId)
  for (const hostId of hostIds) {
    statuses[hostId] = 'queued'
    errors[hostId] = ''
  }
  try {
    const results = await runAgentUpdateBatch(
      hostIds,
      concurrency.value,
      async hostId => {
        statuses[hostId] = 'running'
        await agentsStore.updateAgentBinary(hostId)
      },
      async hostId => {
        await agentsStore.checkAgent(hostId)
      },
    )
    for (const result of results) {
      statuses[result.hostId] = result.ok ? 'success' : 'failed'
      errors[result.hostId] = result.error || ''
    }
  } finally {
    await agentsStore.loadAgents()
    running.value = false
  }
}

function statusKey(hostId: string) {
  const status = statuses[hostId] || 'idle'
  return `settings.agents.bulkUpdateStatus${status[0].toUpperCase()}${status.slice(1)}`
}
</script>

<template>
  <div v-if="visible" class="settings-modal-backdrop" data-test="bulk-update-modal">
    <div class="settings-modal bulk-update-modal">
      <header class="settings-modal-header">
        <div>
          <h2 class="settings-modal-title">{{ t('settings.agents.bulkUpdateTitle') }}</h2>
          <p class="bulk-description">{{ t('settings.agents.bulkUpdateDescription') }}</p>
        </div>
        <button class="settings-btn settings-btn-icon" type="button" :disabled="running" @click="emit('cancel')">×</button>
      </header>

      <div class="settings-modal-body">
        <div v-if="loadError" class="settings-alert settings-alert-danger">{{ loadError }}</div>
        <div class="bulk-toolbar">
          <span class="bulk-target">{{ t('settings.agents.bulkUpdateTarget', { version: targetVersion || '-' }) }}</span>
          <span class="muted">{{ t('settings.agents.bulkUpdateConcurrency', { count: concurrency }) }}</span>
          <button class="settings-btn settings-btn-secondary" type="button" data-test="bulk-update-select-default" :disabled="running" @click="selectDefault">
            {{ t('settings.agents.bulkUpdateSelectDefault') }}
          </button>
          <button class="settings-btn settings-btn-secondary" type="button" data-test="bulk-update-clear" :disabled="running" @click="clearSelection">
            {{ t('settings.agents.bulkUpdateClear') }}
          </button>
        </div>

        <div class="bulk-table">
          <div class="bulk-head">
            <span></span>
            <span>{{ t('settings.agents.host') }}</span>
            <span>{{ t('settings.agents.health') }}</span>
            <span>{{ t('settings.agents.bulkUpdateCurrentVersion') }}</span>
            <span>{{ t('settings.agents.bulkUpdateReason') }}</span>
            <span>{{ t('settings.agents.bulkUpdateRunStatus') }}</span>
          </div>
          <div v-for="row in rows" :key="row.hostId" class="bulk-row" :data-test="`bulk-update-row-${row.hostId}`">
            <input
              type="checkbox"
              :data-test="`bulk-update-checkbox-${row.hostId}`"
              :checked="selected.has(row.hostId)"
              :disabled="running || row.disabled"
              @change="toggle(row, ($event.target as HTMLInputElement).checked)"
            />
            <span><strong>{{ row.hostName }}</strong><br><span class="mono muted">{{ row.hostId }}</span></span>
            <span>{{ row.health }}</span>
            <span>{{ row.currentVersion || t('settings.agents.bulkUpdateVersionUnknown') }}</span>
            <span>{{ t(row.reasonKey) }}</span>
            <span>
              {{ t(statusKey(row.hostId)) }}
              <span v-if="errors[row.hostId]" class="bulk-error">{{ errors[row.hostId] }}</span>
            </span>
          </div>
        </div>
      </div>

      <footer class="settings-modal-footer">
        <span v-if="selectedCount === 0" class="muted">{{ t('settings.agents.bulkUpdateNoSelection') }}</span>
        <button class="settings-btn" type="button" :disabled="running" @click="emit('cancel')">{{ t('common.close') }}</button>
        <button class="settings-btn settings-btn-primary" type="button" data-test="bulk-update-start" :disabled="!canStart" @click="start">
          {{ t('settings.agents.bulkUpdateStart', { count: selectedCount }) }}
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.bulk-update-modal {
  width: min(940px, calc(100vw - 48px));
}
.bulk-description {
  margin: 3px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
}
.bulk-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.bulk-target {
  font-weight: 650;
}
.bulk-table {
  display: grid;
  gap: 4px;
  max-height: 440px;
  overflow: auto;
}
.bulk-head,
.bulk-row {
  display: grid;
  grid-template-columns: 28px minmax(140px, 1.2fr) 90px 110px minmax(150px, 1fr) minmax(120px, 1fr);
  gap: 10px;
  align-items: center;
}
.bulk-head {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 650;
}
.bulk-row {
  padding: 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  font-size: 12px;
}
.bulk-error {
  display: block;
  margin-top: 3px;
  color: var(--status-failed);
}
.mono {
  font-family: var(--font-mono, monospace);
}
.muted {
  color: var(--text-tertiary);
}
</style>
