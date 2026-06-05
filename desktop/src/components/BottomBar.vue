<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { save } from '@tauri-apps/plugin-dialog'
import { usePanelStore, type PanelLeafNode } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { useFilterStore } from '@/stores/filter'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { AGENT_HOST } from '@/api/agent'
import type { LogEntry } from '@/api/agent'
import type { SyncBookmarkCapture, SyncBookmarkPanel } from '@/stores/bookmark'

const agentHost = AGENT_HOST

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()
const deploymentLogStore = useDeploymentLogStore()
const filterStore = useFilterStore()
const operationApprovalStore = useOperationApprovalStore()
const router = useRouter()
const { t } = useI18n()

// leafDeploymentId 取叶子节点订阅的 deploymentId（leaf.serviceId 语义即 deploymentId）。
function leafDeploymentId(leaf: PanelLeafNode): string | null {
  return leaf.source?.type === 'deployment' ? leaf.source.deploymentId : leaf.serviceId
}

// 所有面板中的服务（按 deployment 反查所属 service，去重）。
// checkedIds 内部仍以 deploymentId 为键（命名沿用历史的 service 概念）。
const panelServices = computed(() => {
  const seen = new Set<string>()
  const result: Array<{ id: string; name: string; status: string }> = []
  for (const leaf of panelStore.allLeaves) {
    const deploymentId = leafDeploymentId(leaf)
    if (deploymentId && !seen.has(deploymentId)) {
      seen.add(deploymentId)
      const info = agentStore.serviceForDeployment(deploymentId)
      if (info) {
        result.push({
          id: deploymentId,
          name: `${info.service.name} · ${info.envName}`,
          status: info.deployment.status,
        })
      }
    }
  }
  return result
})

// 底部栏勾选状态（独立于侧边栏 env_selected_service_ids 的启动选中）
const checkedIds = ref<Set<string>>(new Set())
const manuallyTouchedIds = ref<Set<string>>(new Set())
const checkedServiceIds = computed(() =>
  panelServices.value.filter(svc => checkedIds.value.has(svc.id)).map(svc => svc.id),
)

watch(
  panelServices,
  (services) => {
    const visibleIds = new Set(services.map(svc => svc.id))
    const next = new Set([...checkedIds.value].filter(id => visibleIds.has(id)))
    for (const svc of services) {
      if (!manuallyTouchedIds.value.has(svc.id)) next.add(svc.id)
    }
    checkedIds.value = next
  },
  { immediate: true },
)

function toggleCheck(serviceId: string) {
  manuallyTouchedIds.value = new Set(manuallyTouchedIds.value).add(serviceId)
  const next = new Set(checkedIds.value)
  if (next.has(serviceId)) {
    next.delete(serviceId)
  } else {
    next.add(serviceId)
  }
  checkedIds.value = next
}

async function restartChecked() {
  await Promise.all(checkedServiceIds.value.map(id => agentStore.restartDeployment(id)))
}

async function stopChecked() {
  await Promise.all(checkedServiceIds.value.map(id => agentStore.stopDeployment(id)))
}

// 日志录制：底层仍使用同步书签能力，展示层统一为用户可理解的日志录制。
const syncEnabled = computed(() => bookmarkStore.syncEnabled)
const syncRecording = computed(() => bookmarkStore.syncRecording)
const hasSyncOutput = computed(() => bookmarkStore.hasSyncOutput)

function syncPanels(): SyncBookmarkPanel[] {
  return panelStore.allLeaves
    .filter(leaf => leaf.source || leaf.serviceId)
    .map(leaf => ({
      panelId: leaf.id,
      serviceId: leafDeploymentId(leaf),
      source: leaf.source,
    }))
}

function refreshSyncPanelIds() {
  bookmarkStore.syncPanelIds = new Set(syncPanels().map(panel => panel.panelId))
}

function toggleSync() {
  const nextEnabled = !bookmarkStore.syncEnabled
  bookmarkStore.setSyncEnabled(nextEnabled)
  if (nextEnabled) {
    refreshSyncPanelIds()
  }
}

watch(
  () => panelStore.allLeaves.map(leaf => `${leaf.id}:${JSON.stringify(leaf.source)}`).join('|'),
  () => {
    if (bookmarkStore.syncEnabled && !syncRecording.value) refreshSyncPanelIds()
  },
)

function visibleLogsForLeaf(leaf: PanelLeafNode): LogEntry[] {
  const deploymentId = leafDeploymentId(leaf)
  if (deploymentId) return deploymentLogStore.getLogs(deploymentId)
  return []
}

function syncCaptures(): SyncBookmarkCapture[] {
  return [...bookmarkStore.syncPanelIds].map((panelId) => {
    const leaf = panelStore.allLeaves.find(item => item.id === panelId)
    const bm = bookmarkStore.getBookmark(panelId)
    // filter 的项目规则键需要 projectId：通过 deployment 反查所属项目。
    const projectId = leaf
      ? agentStore.serviceForDeployment(leafDeploymentId(leaf) ?? '')?.service.project_id ?? null
      : null
    const captureLogs = leaf
      ? filterStore.applyFilters(panelId, projectId, visibleLogsForLeaf(leaf))
      : undefined
    return {
      panelId,
      captureLogs,
      capturedIds: bm ? new Set(bm.lockedLogs.map(log => log.id)) : undefined,
    }
  })
}

function toggleSyncRecord() {
  for (const panelId of bookmarkStore.syncPanelIds) {
    const leaf = panelStore.allLeaves.find(l => l.id === panelId)
    const deploymentId = leaf ? leafDeploymentId(leaf) : null
    if (deploymentId) deploymentLogStore.closeActiveFoldForDeployment(deploymentId)
  }
  if (syncRecording.value) {
    bookmarkStore.endSyncBookmark(syncCaptures())
  } else {
    const panels = syncPanels()
    if (panels.length === 0) {
      window.alert(t('bottomBar.noSyncPanels'))
      return
    }
    bookmarkStore.startSyncBookmark(panels)
    bookmarkStore.setSyncEnabled(true)
  }
}

async function copySyncBookmarks() {
  const text = bookmarkStore.formatSyncBookmarks()
  if (!text.trim()) {
    window.alert(t('bottomBar.noSyncCopy'))
    return
  }
  await navigator.clipboard.writeText(text)
}

function resolveExportPath(selected: string, defaultName: string): string {
  if (/\.(log|txt)$/i.test(selected)) return selected
  const sep = selected.includes('\\') ? '\\' : '/'
  return selected.endsWith(sep) ? `${selected}${defaultName}` : `${selected}${sep}${defaultName}`
}

async function exportSyncBookmarks() {
  const text = bookmarkStore.formatSyncBookmarks()
  if (!text.trim()) {
    window.alert(t('bottomBar.noSyncExport'))
    return
  }

  const defaultName = `superdev-sync-${Date.now()}.log`
  const selected = await save({
    defaultPath: defaultName,
    title: t('bottomBar.exportTitle'),
    filters: [{ name: 'Log', extensions: ['log', 'txt'] }],
  })
  if (!selected) return

  const filePath = resolveExportPath(selected, defaultName)
  try {
    const { writeTextFile } = await import('@tauri-apps/plugin-fs')
    await writeTextFile(filePath, text)
  } catch (err) {
    console.error('[SuperDev] export sync bookmark failed:', err)
    window.alert(t('common.exportFailed', { message: err instanceof Error ? err.message : String(err) }))
  }
}

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

const connectionText = computed(() =>
  agentStore.connected ? t('bottomBar.connected') : t('bottomBar.disconnected'),
)

function openApprovals() {
  void router.push({ path: '/settings', query: { tab: 'approvals' } })
}

onMounted(() => {
  void operationApprovalStore.loadPending(false)
})
</script>

<template>
  <div class="bottom-bar">
    <section class="bottom-group open-group" data-test="bottom-open-deployments">
      <span class="group-label">{{ t('bottomBar.openDeployments') }}</span>
      <div class="service-chips">
        <div
          v-for="svc in panelServices"
          :key="svc.id"
          class="service-chip"
        >
          <input
            type="checkbox"
            :checked="checkedIds.has(svc.id)"
            @change="toggleCheck(svc.id)"
          />
          <span class="dot" :style="{ background: statusColor(svc.status) }" />
          <span class="svc-name">{{ svc.name }}</span>
        </div>
      </div>
    </section>

    <section class="bottom-group action-group" data-test="bottom-deployment-actions">
      <span class="group-label">{{ t('bottomBar.selectedActions') }}</span>
      <button class="action-btn" :disabled="checkedServiceIds.length === 0" @click="restartChecked">↺ {{ t('bottomBar.restart') }}</button>
      <button class="action-btn danger" :disabled="checkedServiceIds.length === 0" @click="stopChecked">⏹ {{ t('bottomBar.stop') }}</button>
    </section>

    <section class="bottom-group evidence-group" data-test="bottom-evidence">
      <label class="sync-label">
        <input
          data-test="sync-toggle"
          type="checkbox"
          :checked="syncEnabled"
          @change="toggleSync"
        />
        <span>{{ t('bottomBar.syncRecording') }}</span>
      </label>
      <button
        v-if="syncEnabled"
        data-test="sync-record"
        class="sync-record-btn"
        :class="{ recording: syncRecording }"
        :title="syncRecording ? t('bottomBar.stopRecord') : t('bottomBar.record')"
        @click="toggleSyncRecord"
      >
        {{ syncRecording ? '⏹' : '⏺' }}
      </button>
      <template v-if="syncEnabled && hasSyncOutput && !syncRecording">
        <button
          data-test="sync-copy"
          class="sync-icon-btn sync-copy-btn"
          :title="t('bottomBar.copy')"
          @click="copySyncBookmarks"
        >
          ⎘
        </button>
        <button
          data-test="sync-export"
          class="sync-icon-btn sync-export-btn"
          :title="t('bottomBar.export')"
          @click="exportSyncBookmarks"
        >
          ↑
        </button>
      </template>
    </section>

    <section class="bottom-group runtime-group" data-test="bottom-runtime-status">
      <span class="group-label">{{ t('bottomBar.runtimeStatus') }}</span>
      <div data-test="agent-status" class="agent-status" :title="agentHost">
        <span class="agent-dot" :class="{ connected: agentStore.connected }" />
        <span>{{ t('bottomBar.agent') }}</span>
        <span class="status-meta">{{ connectionText }}</span>
      </div>
      <div data-test="mcp-status" class="mcp-status">
        <span class="agent-dot" :class="{ connected: agentStore.connected }" />
        <span>{{ t('bottomBar.mcp') }}</span>
      </div>
      <button
        type="button"
        data-test="approvals-entry"
        class="approvals-entry"
        :class="{ attention: operationApprovalStore.pendingCount > 0 }"
        @click="openApprovals"
      >
        <span>{{ t('bottomBar.approvals') }}</span>
        <span v-if="operationApprovalStore.pendingCount > 0" class="approval-count">
          {{ operationApprovalStore.pendingCount }}
        </span>
      </button>
    </section>
  </div>
</template>

<style scoped>
.bottom-bar {
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 72px;
  padding: 10px 14px;
  border-top: 1px solid var(--border-secondary);
  background: rgba(13, 22, 30, 0.98);
  color: var(--text-secondary);
  font-size: 12px;
  overflow-x: auto;
  flex-shrink: 0;
}

.bottom-group {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  padding-right: 14px;
  border-right: 1px solid rgba(139, 148, 158, 0.18);
  flex-shrink: 0;
}

.bottom-group:last-child {
  border-right: 0;
  padding-right: 0;
  margin-left: auto;
}

.group-label {
  color: var(--text-secondary);
  font-size: 11px;
  white-space: nowrap;
}

.service-chips {
  display: flex;
  align-items: center;
  gap: 8px;
}

.service-chip,
.action-btn,
.sync-label,
.sync-record-btn,
.sync-icon-btn,
.agent-status,
.mcp-status,
.approvals-entry {
  height: 32px;
  display: inline-flex;
  align-items: center;
  border-radius: 6px;
}

.service-chip {
  gap: 6px;
  padding: 0 10px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  background: rgba(255, 255, 255, 0.035);
}

.service-chip input,
.sync-label input {
  accent-color: #1f6feb;
  width: 13px;
  height: 13px;
  cursor: pointer;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.svc-name {
  color: var(--text-primary);
  font-size: 11px;
  white-space: nowrap;
}

.action-btn {
  gap: 5px;
  padding: 0 10px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  font-size: 11px;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}

.action-btn:hover {
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-primary);
}

.action-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.action-btn.danger {
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}

.sync-label {
  gap: 6px;
  padding: 0 9px;
  border: 1px solid rgba(139, 148, 158, 0.2);
  background: rgba(255, 255, 255, 0.03);
  font-size: 11px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}

.sync-record-btn {
  justify-content: center;
  width: 32px;
  border: 1px solid rgba(63, 185, 80, 0.28);
  background: rgba(63, 185, 80, 0.08);
  color: #3fb950;
  font-size: 14px;
  cursor: pointer;
  line-height: 1;
  flex-shrink: 0;
}

.sync-record-btn.recording {
  border-color: rgba(248, 81, 73, 0.32);
  background: rgba(248, 81, 73, 0.08);
  color: #f85149;
}

.sync-icon-btn {
  justify-content: center;
  width: 32px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
}

.sync-icon-btn:hover {
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-primary);
}

.runtime-group {
  gap: 10px;
}

.agent-status,
.mcp-status {
  gap: 5px;
  padding: 0 2px;
  color: var(--text-tertiary);
  font-size: 11px;
  white-space: nowrap;
  flex-shrink: 0;
}

.status-meta {
  color: var(--text-secondary);
}

.agent-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #6e7681;
}

.agent-dot.connected {
  background: #3fb950;
}

.approvals-entry {
  gap: 5px;
  padding: 0 9px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}

.approvals-entry:hover {
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-primary);
}

.approvals-entry.attention {
  border-color: rgba(210,153,34,0.35);
  color: #d29922;
}

.approval-count {
  min-width: 16px;
  height: 16px;
  padding: 0 5px;
  border-radius: 999px;
  background: rgba(210,153,34,0.16);
  color: #d29922;
  font-size: 10px;
  line-height: 16px;
  text-align: center;
}
</style>
