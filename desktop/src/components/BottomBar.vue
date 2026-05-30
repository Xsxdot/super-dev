<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { usePanelStore, type PanelLeafNode } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { useFilterStore } from '@/stores/filter'
import { AGENT_HOST } from '@/api/agent'
import type { LogEntry } from '@/api/agent'

const agentHost = AGENT_HOST
import type { SyncBookmarkCapture, SyncBookmarkPanel } from '@/stores/bookmark'

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()
const deploymentLogStore = useDeploymentLogStore()
const filterStore = useFilterStore()

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

// 同步录制
const syncEnabled = ref(false)
const syncRecording = computed(() => bookmarkStore.syncRecording)
const hasSyncOutput = computed(() => bookmarkStore.formatSyncBookmarks().trim().length > 0)

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
  syncEnabled.value = !syncEnabled.value
  if (syncEnabled.value) {
    refreshSyncPanelIds()
  } else if (!syncRecording.value) {
    bookmarkStore.syncPanelIds = new Set()
  }
}

watch(
  () => panelStore.allLeaves.map(leaf => `${leaf.id}:${JSON.stringify(leaf.source)}`).join('|'),
  () => {
    if (syncEnabled.value && !syncRecording.value) refreshSyncPanelIds()
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
      window.alert('没有可同步录制的面板')
      return
    }
    bookmarkStore.startSyncBookmark(panels)
    syncEnabled.value = true
  }
}

async function copySyncBookmarks() {
  const text = bookmarkStore.formatSyncBookmarks()
  if (!text.trim()) {
    window.alert('同步录制区间内没有可复制的日志')
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
    window.alert('同步录制区间内没有可导出的日志')
    return
  }

  const defaultName = `superdev-sync-${Date.now()}.log`
  const { save } = await import('@tauri-apps/plugin-dialog')
  const selected = await save({
    defaultPath: defaultName,
    title: '导出同步录制日志',
    filters: [{ name: 'Log', extensions: ['log', 'txt'] }],
  })
  if (!selected) return

  const filePath = resolveExportPath(selected, defaultName)
  try {
    const { writeTextFile } = await import('@tauri-apps/plugin-fs')
    await writeTextFile(filePath, text)
  } catch (err) {
    console.error('[SuperDev] export sync bookmark failed:', err)
    window.alert(`导出失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}
</script>

<template>
  <div class="bottom-bar">
    <span class="label">面板服务</span>

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
          style="accent-color: #1f6feb; width: 11px; height: 11px; cursor: pointer;"
        />
        <span class="dot" :style="{ background: statusColor(svc.status) }" />
        <span class="svc-name">{{ svc.name }}</span>
      </div>
    </div>

    <template v-if="checkedServiceIds.length > 0">
      <div class="divider" />
      <button class="action-btn" @click="restartChecked">↺ 重启</button>
      <button class="action-btn danger" @click="stopChecked">⏹ 停止</button>
    </template>

    <div class="divider" />

    <!-- 同步录制 -->
    <label class="sync-label">
      <input type="checkbox" :checked="syncEnabled" @change="toggleSync" style="accent-color:#1f6feb;" />
      <span>同步录制</span>
    </label>
    <button
      v-if="syncEnabled"
      class="sync-record-btn"
      :class="{ recording: syncRecording }"
      @click="toggleSyncRecord"
    >
      {{ syncRecording ? '⏹' : '⏺' }}
    </button>
    <template v-if="hasSyncOutput && !syncRecording">
      <button class="action-btn sync-copy-btn" @click="copySyncBookmarks">复制</button>
      <button class="action-btn sync-export-btn" @click="exportSyncBookmarks">导出</button>
    </template>

    <div class="flex-1" />

    <!-- Agent 状态 -->
    <div class="agent-status">
      <span class="agent-dot" :class="{ connected: agentStore.connected }" />
      <span>{{ agentHost }}</span>
    </div>
  </div>
</template>

<style scoped>
.bottom-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  background: var(--bg-elevated);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  min-height: 30px;
  overflow-x: auto;
}
.label {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
  flex-shrink: 0;
}
.service-chips { display: flex; gap: 10px; align-items: center; }
.service-chip { display: flex; align-items: center; gap: 4px; }
.dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.svc-name { font-size: 11px; color: var(--text-primary); white-space: nowrap; }

.divider { width: 1px; height: 14px; background: var(--border); flex-shrink: 0; }
.flex-1 { flex: 1; }

.action-btn {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.action-btn:hover { background: var(--bg-overlay); }
.action-btn.danger {
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}

.sync-label {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.sync-record-btn {
  background: transparent;
  border: none;
  font-size: 15px;
  cursor: pointer;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
  color: #3fb950;
}
.sync-record-btn.recording { color: #f85149; }

.agent-status {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 10px;
  color: var(--text-tertiary);
  white-space: nowrap;
  flex-shrink: 0;
}
.agent-dot {
  width: 6px; height: 6px;
  border-radius: 50%;
  background: #6e7681;
}
.agent-dot.connected { background: #3fb950; }
</style>
