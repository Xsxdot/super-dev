<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { usePanelStore, type PanelLeafNode } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useDeploymentNodeSelectionStore } from '@/stores/deploymentNodeSelection'
import { useLogEvidenceStore } from '@/stores/logEvidence'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import OperationApprovalPopover from '@/components/OperationApprovalPopover.vue'
import EvidenceDrawer from '@/components/Evidence/EvidenceDrawer.vue'
import { useRemoteStore } from '@/stores/remote'
import { useNodeStore } from '@/stores/node'
import { usePortMirrorStore } from '@/stores/portMirror'
import {
  buildDeploymentNodeStatus,
  type DeploymentAggregateNodeStatus,
  type DeploymentNodeIssueKind,
  type DeploymentNodeState,
} from '@/lib/deploymentNodeStatus'
import { mirrorRowsForDeployment } from '@/lib/portMirrorView'
import { AGENT_HOST } from '@/api/agent'
import type { Deployment } from '@/api/agent'

const agentHost = AGENT_HOST

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const deploymentNodeSelectionStore = useDeploymentNodeSelectionStore()
const evidenceStore = useLogEvidenceStore()
const operationApprovalStore = useOperationApprovalStore()
const remoteStore = useRemoteStore()
const nodeStore = useNodeStore()
const portMirrorStore = usePortMirrorStore()
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

/** MirrorChip 是端口镜像 group 单个 chip 的呈现数据，key 用于 v-for，其余字段直接渲染。 */
interface MirrorChip {
  key: string
  port: number
  hostName: string
  conflict: boolean
}

// mirrorChips 端口镜像 group 用的 chip 列表：只看当前已在面板打开的 deployment
// （与 open-group/log-display-group 同一套"打开的部署"作用域，不是项目里全部镜像），
// 每个 deployment 只取 active/conflict 两种稳定态——pending/纯技术性 failed 不进 chip，
// 与 mirrorRowsForDeployment 的调用方约定一致（见 lib/portMirrorView.ts）。
const mirrorChips = computed<MirrorChip[]>(() => {
  const chips: MirrorChip[] = []
  for (const svc of panelServices.value) {
    for (const row of mirrorRowsForDeployment(svc.id, portMirrorStore.mirrors)) {
      if (row.state !== 'active' && !row.conflict) continue
      chips.push({ key: `${svc.id}:${row.hostId}:${row.port}`, port: row.port, hostName: row.hostName, conflict: row.conflict })
    }
  }
  return chips
})

function mirrorDotColor(conflict: boolean): string {
  return conflict ? 'var(--status-warning)' : 'var(--status-running)'
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

// 已连接时区分 attach（服务化安装/headless agent）与 sidecar：
// attach 场景下附带展示对端 agent 版本，帮助用户确认接的是哪个安装。
const connectionText = computed(() => {
  if (!agentStore.connected) return t('bottomBar.disconnected')
  const info = agentStore.connectionInfo
  if (info?.mode === 'attached') {
    return info.version
      ? t('bottomBar.connectedAttached', { version: info.version })
      : t('bottomBar.connectedAttachedNoVersion')
  }
  return t('bottomBar.connected')
})

async function toggleApprovalsPopover() {
  approvalsPopoverOpen.value = !approvalsPopoverOpen.value
  if (approvalsPopoverOpen.value) await operationApprovalStore.loadPending(false)
}

function openApprovals() {
  approvalsPopoverOpen.value = false
  void router.push({ path: '/settings', query: { tab: 'approvals' } })
}

onMounted(() => {
  window.addEventListener('resize', updateLogDisplayMenuPosition)
})

onBeforeUnmount(() => {
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
      <span class="group-label">{{ t('bottomBar.evidence') }}</span>
      <!-- 旧 bookmark 同步录制仍保留在 store 中兼容，BottomBar 的可见入口迁移为 Evidence 工作台。 -->
      <button
        type="button"
        data-test="bottom-evidence-open"
        class="evidence-open-btn"
        :aria-expanded="evidenceStore.drawerOpen"
        @click="evidenceStore.setDrawerOpen(!evidenceStore.drawerOpen)"
      >
        {{ t('bottomBar.evidencePins', { count: evidenceStore.activePins.length }) }}
      </button>
      <EvidenceDrawer v-if="evidenceStore.drawerOpen" />
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

    <!-- 端口镜像 group：新增，放在运行状态 group 之前。证据 group（上方）不受影响——
         这是追加的新 group，不是替换任何既有 group。 -->
    <section
      v-if="mirrorChips.length > 0"
      class="bottom-group mirror-group"
      data-test="bottom-mirror"
    >
      <span class="group-label">{{ t('bottomBar.mirror.groupLabel') }}</span>
      <div class="mirror-chips">
        <div
          v-for="chip in mirrorChips"
          :key="chip.key"
          class="service-chip mirror-chip"
          data-test="bottom-mirror-chip"
          :title="chip.conflict ? t('bottomBar.mirror.conflict') : t('bottomBar.mirror.active')"
        >
          <span class="dot" :style="{ background: mirrorDotColor(chip.conflict) }" />
          <span class="svc-name">:{{ chip.port }} ⇄ {{ chip.hostName }}</span>
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

.mirror-chips {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 复用 .service-chip 的盒模型（边框/背景/高度/圆角），这里没有勾选框，所以去掉
   继承来的 pointer 光标——chip 本身不可点击，只是状态展示。 */
.mirror-chip {
  cursor: default;
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
.evidence-open-btn,
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

.evidence-open-btn {
  gap: 6px;
  padding: 0 10px;
  border: 1px solid rgba(88, 166, 255, 0.28);
  background: rgba(88, 166, 255, 0.08);
  color: #58a6ff;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}

.evidence-open-btn:hover {
  background: rgba(88, 166, 255, 0.14);
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
