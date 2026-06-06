<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { save } from '@tauri-apps/plugin-dialog'
import { usePanelStore, type PanelLeafNode } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { useDeploymentNodeSelectionStore } from '@/stores/deploymentNodeSelection'
import { useFilterStore } from '@/stores/filter'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import OperationApprovalPopover from '@/components/OperationApprovalPopover.vue'
import { useRemoteStore } from '@/stores/remote'
import { useNodeStore } from '@/stores/node'
import {
  buildDeploymentNodeStatus,
  type DeploymentAggregateNodeStatus,
  type DeploymentNodeIssueKind,
  type DeploymentNodeState,
} from '@/lib/deploymentNodeStatus'
import { AGENT_HOST } from '@/api/agent'
import type { Deployment, LogEntry } from '@/api/agent'
import type { SyncBookmarkCapture, SyncBookmarkPanel } from '@/stores/bookmark'

const agentHost = AGENT_HOST

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()
const deploymentLogStore = useDeploymentLogStore()
const deploymentNodeSelectionStore = useDeploymentNodeSelectionStore()
const filterStore = useFilterStore()
const operationApprovalStore = useOperationApprovalStore()
const remoteStore = useRemoteStore()
const nodeStore = useNodeStore()
const router = useRouter()
const { t } = useI18n()
const approvalsPopoverOpen = ref(false)
const logDisplayOpen = ref(false)
const logDisplaySelectRef = ref<HTMLElement | null>(null)
const logDisplayMenuStyle = ref<Record<string, string>>({})
const remoteManagedStatuses = computed(() =>
  nodeStore.managedStatuses.size > 0 ? nodeStore.managedStatuses : remoteStore.managedStatuses,
)

// leafDeploymentId 取叶子节点订阅的 deploymentId（leaf.serviceId 语义即 deploymentId）。
function leafDeploymentId(leaf: PanelLeafNode): string | null {
  return leaf.source?.type === 'deployment' ? leaf.source.deploymentId : leaf.serviceId
}

// 所有面板中的服务（按 deployment 反查所属 service，去重）。
// checkedIds 内部仍以 deploymentId 为键（命名沿用历史的 service 概念）。
const panelServices = computed(() => {
  const seen = new Set<string>()
  const result: Array<{
    id: string
    name: string
    status: string
    deployment: Deployment
    aggregate: DeploymentAggregateNodeStatus
  }> = []
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
          deployment: info.deployment,
          aggregate: buildDeploymentNodeStatus(info.deployment, remoteStore.hosts, remoteManagedStatuses.value),
        })
      }
    }
  }
  return result
})

const remoteHostIds = computed(() => {
  const ids: string[] = []
  for (const leaf of panelStore.allLeaves) {
    const deploymentId = leafDeploymentId(leaf)
    if (!deploymentId) continue
    const info = agentStore.serviceForDeployment(deploymentId)
    if (info?.deployment.location === 'remote') ids.push(...(info.deployment.host_ids ?? []))
  }
  return [...new Set(ids)]
})

async function refreshRemoteNodeContext(hostIds: string[]) {
  if (hostIds.length === 0) return
  try {
    if (remoteStore.hosts.length === 0) await remoteStore.loadHosts()
    if (nodeStore.managedStatuses.size > 0) return
    await remoteStore.refreshManagedStatuses(hostIds)
  } catch (err) {
    console.warn('[SuperDev] refresh bottom remote node status failed:', err)
  }
}

watch(
  remoteHostIds,
  hostIds => void refreshRemoteNodeContext(hostIds),
  { immediate: true },
)

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
      if (svc.deployment.location === 'remote') {
        deploymentNodeSelectionStore.ensureDeploymentNodes(
          svc.id,
          svc.aggregate.nodes.map(node => node.hostId),
        )
      }
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

const nodeHealthColor = (health: string) => {
  if (health === 'healthy') return '#3fb950'
  if (health === 'warning') return '#d29922'
  if (health === 'failed') return '#f85149'
  return '#6e7681'
}

function deploymentDotColor(svc: { deployment: Deployment; status: string; aggregate: DeploymentAggregateNodeStatus }) {
  return svc.deployment.location === 'remote' ? nodeHealthColor(svc.aggregate.health) : statusColor(svc.status)
}

function issueLabel(kind: DeploymentNodeIssueKind, detail?: string): string {
  if (kind === 'host-error') return detail || t('bottomBar.nodeHostError')
  if (kind === 'collector-error') return detail || t('bottomBar.nodeCollectorError')
  return t(`bottomBar.nodeIssues.${kind}`)
}

function nodeIssueLabel(node: DeploymentNodeState): string {
  if (!node.issue) return t('bottomBar.nodeHealthy')
  return issueLabel(node.issue.kind, node.issue.detail)
}

function remoteNodesOf(svc: { deployment: Deployment; aggregate: DeploymentAggregateNodeStatus }) {
  return svc.deployment.location === 'remote' ? svc.aggregate.nodes : []
}

const logDisplayServices = computed(() =>
  panelServices.value.filter(svc => svc.deployment.location === 'remote' && remoteNodesOf(svc).length > 0),
)

const logDisplayTotalNodes = computed(() =>
  logDisplayServices.value.reduce((sum, svc) => sum + remoteNodesOf(svc).length, 0),
)

const selectedLogDisplayNodeCount = computed(() =>
  logDisplayServices.value.reduce((sum, svc) => sum + selectedNodeIds(svc.id).length, 0),
)

const logDisplayHealth = computed(() => {
  const services = logDisplayServices.value
  if (services.some(svc => svc.aggregate.health === 'failed')) return 'failed'
  if (services.some(svc => svc.aggregate.health === 'warning')) return 'warning'
  if (services.length > 0 && services.every(svc => svc.aggregate.health === 'healthy')) return 'healthy'
  return 'unknown'
})

const logDisplaySummary = computed(() => {
  const total = logDisplayTotalNodes.value
  if (total === 0) return ''
  if (logDisplayServices.value.length > 1) {
    return t('bottomBar.logDisplayServiceScope', {
      services: logDisplayServices.value.length,
      selected: selectedLogDisplayNodeCount.value,
      total,
    })
  }
  return t('bottomBar.nodeScope', { selected: selectedLogDisplayNodeCount.value, total })
})

function selectedNodeIds(deploymentId: string): string[] {
  return deploymentNodeSelectionStore.selectedHostIds(deploymentId)
}

function isNodeSelected(deploymentId: string, hostId: string): boolean {
  return deploymentNodeSelectionStore.isNodeSelected(deploymentId, hostId)
}

function toggleNode(deploymentId: string, hostId: string) {
  deploymentNodeSelectionStore.toggleNode(deploymentId, hostId)
}

function updateLogDisplayMenuPosition() {
  const el = logDisplaySelectRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const width = 320
  const viewportPadding = 8
  const left = Math.min(
    Math.max(viewportPadding, rect.left),
    Math.max(viewportPadding, window.innerWidth - width - viewportPadding),
  )
  logDisplayMenuStyle.value = {
    left: `${left}px`,
    bottom: `${Math.max(viewportPadding, window.innerHeight - rect.top + 8)}px`,
    width: `${width}px`,
  }
}

async function toggleLogDisplay() {
  if (logDisplayTotalNodes.value === 0) return
  logDisplayOpen.value = !logDisplayOpen.value
  if (logDisplayOpen.value) {
    await nextTick()
    updateLogDisplayMenuPosition()
  }
}

function toggleLogDisplayNode(deploymentId: string, hostId: string) {
  toggleNode(deploymentId, hostId)
}

const connectionText = computed(() =>
  agentStore.connected ? t('bottomBar.connected') : t('bottomBar.disconnected'),
)

async function toggleApprovalsPopover() {
  approvalsPopoverOpen.value = !approvalsPopoverOpen.value
  if (approvalsPopoverOpen.value) await operationApprovalStore.loadPending(false)
}

function openApprovals() {
  approvalsPopoverOpen.value = false
  void router.push({ path: '/settings', query: { tab: 'approvals' } })
}

onMounted(() => {
  void operationApprovalStore.loadPending(false)
  operationApprovalStore.startPolling()
  window.addEventListener('resize', updateLogDisplayMenuPosition)
})

onBeforeUnmount(() => {
  operationApprovalStore.stopPolling()
  window.removeEventListener('resize', updateLogDisplayMenuPosition)
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
          class="deployment-node-group"
          data-test="bottom-deployment-node-group"
        >
          <label class="service-chip">
            <input
              type="checkbox"
              :checked="checkedIds.has(svc.id)"
              @change="toggleCheck(svc.id)"
            />
            <span class="dot" :style="{ background: deploymentDotColor(svc) }" />
            <span class="svc-name">{{ svc.name }}</span>
          </label>
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

    <section
      v-if="logDisplayServices.length > 0"
      class="bottom-group log-display-group"
      data-test="bottom-log-display"
      @click.stop
    >
      <span class="group-label">{{ t('bottomBar.logDisplay') }}</span>
      <div ref="logDisplaySelectRef" class="log-display-select">
        <button
          type="button"
          class="log-display-toggle"
          data-test="bottom-log-display-toggle"
          :aria-expanded="logDisplayOpen"
          @click="toggleLogDisplay"
        >
          <span
            class="dot"
            :style="{ background: nodeHealthColor(logDisplayHealth) }"
          />
          <span>{{ logDisplaySummary }}</span>
          <span class="select-caret">▾</span>
        </button>
        <div
          v-if="logDisplayOpen"
          class="log-display-menu"
          data-test="bottom-log-display-menu"
          :style="logDisplayMenuStyle"
        >
          <div
            v-for="svc in logDisplayServices"
            :key="svc.id"
            class="log-display-service"
          >
            <div
              class="log-display-service-head"
              data-test="bottom-log-display-service"
            >
              <span class="dot" :style="{ background: nodeHealthColor(svc.aggregate.health) }" />
              <span class="log-display-service-name">{{ svc.name }}</span>
              <span class="log-display-service-count">
                {{ selectedNodeIds(svc.id).length }}/{{ remoteNodesOf(svc).length }}
              </span>
            </div>
            <label
              v-for="node in remoteNodesOf(svc)"
              :key="`${svc.id}:${node.hostId}`"
              class="log-display-option"
              data-test="bottom-log-display-option"
              :title="nodeIssueLabel(node)"
            >
              <input
                type="checkbox"
                :checked="isNodeSelected(svc.id, node.hostId)"
                @change="toggleLogDisplayNode(svc.id, node.hostId)"
              />
              <span class="dot" :style="{ background: nodeHealthColor(node.health) }" />
              <span class="node-name">{{ node.hostName }}</span>
              <span class="node-issue">{{ nodeIssueLabel(node) }}</span>
            </label>
          </div>
        </div>
      </div>
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
        :aria-expanded="approvalsPopoverOpen"
        @click="toggleApprovalsPopover"
      >
        <span>{{ t('bottomBar.approvals') }}</span>
        <span v-if="operationApprovalStore.pendingCount > 0" class="approval-count">
          {{ operationApprovalStore.pendingCount }}
        </span>
      </button>
      <OperationApprovalPopover
        v-if="approvalsPopoverOpen"
        @view-all="openApprovals"
      />
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

.deployment-node-group {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.service-chip,
.node-chip,
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
  cursor: pointer;
}

.service-chip input,
.node-chip input,
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

.node-chip-list {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 360px;
  overflow-x: auto;
  padding-bottom: 1px;
}

.node-scope {
  color: var(--text-tertiary);
  font-size: 10px;
  white-space: nowrap;
}

.node-chip {
  gap: 5px;
  padding: 0 8px;
  border: 1px solid rgba(139, 148, 158, 0.18);
  background: rgba(255, 255, 255, 0.025);
  color: var(--text-tertiary);
  cursor: pointer;
  white-space: nowrap;
}

.node-chip.selected {
  border-color: rgba(88, 166, 255, 0.34);
  background: rgba(31, 111, 235, 0.1);
  color: var(--text-primary);
}

.node-name {
  max-width: 92px;
  overflow: hidden;
  font-size: 11px;
  text-overflow: ellipsis;
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

.log-display-group {
  position: relative;
}

.log-display-select {
  position: relative;
}

.log-display-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 700;
  cursor: pointer;
  white-space: nowrap;
}

.log-display-toggle:hover {
  border-color: rgba(88, 166, 255, 0.34);
  background: rgba(31, 111, 235, 0.08);
}

.select-caret {
  color: var(--text-tertiary);
  font-size: 10px;
}

.log-display-menu {
  position: fixed;
  z-index: 1000;
  min-width: 300px;
  max-width: 380px;
  padding: 6px;
  border: 1px solid rgba(88, 166, 255, 0.34);
  border-radius: 7px;
  background: rgba(13, 24, 34, 0.98);
  box-shadow: 0 16px 36px rgba(0, 0, 0, 0.42);
}

.log-display-service + .log-display-service {
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px solid rgba(139, 148, 158, 0.13);
}

.log-display-service-head {
  display: grid;
  grid-template-columns: 7px minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  min-height: 26px;
  padding: 0 7px;
  color: var(--text-primary);
  font-size: 11px;
  font-weight: 700;
}

.log-display-service-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-display-service-count {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
}

.log-display-option {
  display: grid;
  grid-template-columns: 16px 7px minmax(72px, 1fr) minmax(0, 1fr);
  align-items: center;
  gap: 7px;
  min-height: 30px;
  padding: 0 7px 0 18px;
  border-radius: 5px;
  color: var(--text-secondary);
  cursor: pointer;
}

.log-display-option:hover {
  background: rgba(255, 255, 255, 0.055);
  color: var(--text-primary);
}

.log-display-option input {
  accent-color: #1f6feb;
  width: 13px;
  height: 13px;
  cursor: pointer;
}

.node-issue {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-group {
  gap: 10px;
  position: relative;
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
