<!--
EnvGroup：侧边栏 Environment 分组。

职责：
  - 展示一个环境名称作为可折叠的分组标题，标题右侧有启动/搜索/停止操作按钮
  - 列出该环境下有 deployment 的 service 行，支持拖拽到面板区域
  - 点击 service 行 emit open-deployment

边界：
  - 不管理折叠以外的任何状态，服务列表由父组件传入
  - 不直接操作 panel store，通过 emit 交给父组件
  - 启动/停止直接调 agentStore，搜索通过 emit search 交给父组件
-->

<script setup lang="ts">
import { ref, computed, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'
import { useNodeStore } from '@/stores/node'
import { useDragDrop } from '@/composables/useDragDrop'
import {
  buildDeploymentNodeStatus,
  type DeploymentAggregateNodeStatus,
  type DeploymentNodeIssueKind,
  type DeploymentNodeState,
} from '@/lib/deploymentNodeStatus'
import type { Deployment, RuntimeInstanceStatus, Service } from '@/api/agent'

const props = defineProps<{
  envName: string
  isDev: boolean
  initiallyExpanded?: boolean
  projectId: string
  services: Service[]
  // selectedServiceIds 语义为「已在面板打开的 deploymentId 集合」，用于行高亮。
  selectedServiceIds: Set<string>
}>()

const emit = defineEmits<{
  'open-deployment': [payload: { deploymentId: string; title: string }]
  'search': []
}>()

const agentStore = useAgentStore()
const remoteStore = useRemoteStore()
const nodeStore = useNodeStore()
const { t } = useI18n()
const { startServiceDrag, moveServiceDrag, endServiceDrag, finishServiceDrag } = useDragDrop()
const remoteManagedStatuses = computed(() =>
  nodeStore.managedStatuses.size > 0 ? nodeStore.managedStatuses : remoteStore.managedStatuses,
)

async function onCheckChange(svc: Service) {
  if (svc.required) return
  const project = agentStore.projects.find(p => p.id === props.projectId)
  if (!project) return
  const current = project.env_selected_service_ids?.[props.envName] ?? []
  const isSelected = agentStore.isServiceEnvSelected(props.projectId, props.envName, svc.name)
  const next = isSelected
    ? current.filter((n: string) => n !== svc.name)
    : [...current, svc.name]
  await agentStore.putEnvSelected(props.projectId, props.envName, next)
}

// dev 环境和父组件指定的首个环境默认展开，避免没有可拖拽服务行。
const expanded = ref(props.initiallyExpanded || props.isDev)
const expandedDeploymentNodes = ref<Set<string>>(new Set())

function toggleExpanded() {
  expanded.value = !expanded.value
}

/**
 * statusColor 根据 deployment 状态返回对应的颜色值。
 */
function statusColor(status: string): string {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

function nodeHealthColor(health: string): string {
  if (health === 'healthy') return '#3fb950'
  if (health === 'warning') return '#d29922'
  if (health === 'failed') return '#f85149'
  return '#6e7681'
}

function isRunningStatus(status: string): boolean {
  return status === 'running' || status === 'starting'
}

function isRuntimeHealthRunning(health: string | undefined): boolean {
  return health === 'running' || health === 'healthy' || health === 'restarting' || health === 'starting'
}

/**
 * deploymentForService 取出本 env 下 service 对应的 deployment。
 * deployment_id 是系统唯一日志单元，一个 service 在一个 env 下对应一个 deployment。
 */
function deploymentForService(svc: Service) {
  return svc.deployments?.find(d => d.env_name === props.envName)
}

function deploymentHostIds(dep: Deployment | undefined): string[] {
  return [...new Set((dep?.host_ids ?? []).map(id => id.trim()).filter(Boolean))]
}

function singleRemoteHostId(dep: Deployment | undefined): string | null {
  if (!dep || dep.location !== 'remote') return null
  const hostIds = deploymentHostIds(dep)
  return hostIds.length === 1 ? hostIds[0] : null
}

const remoteHostIds = computed(() => {
  const ids = props.services
    .map(svc => deploymentForService(svc))
    .filter((dep): dep is Deployment => !!dep && dep.location === 'remote')
    .flatMap(dep => dep.host_ids ?? [])
  return [...new Set(ids)]
})

async function refreshRemoteNodeContext(hostIds: string[]) {
  if (hostIds.length === 0) return
  try {
    if (remoteStore.hosts.length === 0) await remoteStore.loadHosts()
    if (nodeStore.managedStatuses.size > 0) return
    await remoteStore.refreshManagedStatuses(hostIds)
  } catch (err) {
    console.warn('[SuperDev] refresh sidebar remote node status failed:', err)
  }
}

watch(
  remoteHostIds,
  hostIds => void refreshRemoteNodeContext(hostIds),
  { immediate: true },
)

function deploymentNodeStatusForService(svc: Service): DeploymentAggregateNodeStatus | null {
  const dep = deploymentForService(svc)
  if (!dep) return null
  return buildDeploymentNodeStatus(dep, remoteStore.hosts, remoteManagedStatuses.value)
}

function remoteRuntimeInstanceForHost(svc: Service, hostId: string): RuntimeInstanceStatus | undefined {
  const dep = deploymentForService(svc)
  if (!dep || dep.location !== 'remote') return undefined
  return nodeStore.nodeOf(hostId)?.deployments?.find(instance => instance.deployment_id === dep.id)
}

function remoteNodeStateForHost(svc: Service, hostId: string): DeploymentNodeState | undefined {
  return deploymentNodeStatusForService(svc)?.nodes.find(node => node.hostId === hostId)
}

function isRemoteHostRunningForActions(svc: Service, hostId: string): boolean {
  const instance = remoteRuntimeInstanceForHost(svc, hostId)
  if (instance) return isRuntimeHealthRunning(instance.metrics.health)
  const node = remoteNodeStateForHost(svc, hostId)
  // 某些远端 agent 只上报 managed collector 聚合，还没有 runtime instance。
  // 这时 Collector 正在运行是侧边栏已有的最强运行证据，避免绿色节点仍显示“启动”。
  if (node?.collectorExpected) return node.collectorReady
  return isRunningStatus(deploymentForService(svc)?.status ?? '')
}

function isServiceRunningForActions(svc: Service): boolean {
  const dep = deploymentForService(svc)
  if (!dep) return false
  if (dep.location !== 'remote') return isRunningStatus(dep.status)
  const hostIds = deploymentHostIds(dep)
  if (hostIds.length === 0) return isRunningStatus(dep.status)
  return hostIds.some(hostId => isRemoteHostRunningForActions(svc, hostId))
}

function showServiceRowActions(svc: Service): boolean {
  const dep = deploymentForService(svc)
  if (!dep || !canControlDeployment(svc)) return false
  return dep.location !== 'remote' || deploymentHostIds(dep).length <= 1
}

function deploymentNodeSummary(status: DeploymentAggregateNodeStatus): string {
  if (status.total === 0) return t('shell.env.remoteNodeEmpty')
  if (status.collectorExpected > 0) {
    return t('shell.env.remoteNodeSummary', {
      ready: status.ready,
      total: status.total,
      collectors: status.collectorReady,
      desiredCollectors: status.collectorExpected,
    })
  }
  return t('shell.env.remoteNodeSummaryNoCollector', { ready: status.ready, total: status.total })
}

function serviceStatusColor(svc: Service): string {
  const dep = deploymentForService(svc)
  if (!dep) return statusColor('')
  if (dep.location === 'remote') {
    return nodeHealthColor(deploymentNodeStatusForService(svc)?.health ?? 'unknown')
  }
  return statusColor(dep.status)
}

function issueLabel(kind: DeploymentNodeIssueKind, detail?: string): string {
  if (kind === 'host-error') return detail || t('shell.env.nodeHostError')
  if (kind === 'collector-error') return detail || t('shell.env.nodeCollectorError')
  return t(`shell.env.nodeIssues.${kind}`)
}

function nodeIssueLabel(node: DeploymentNodeState): string {
  if (!node.issue) return t('shell.env.nodeHealthy')
  return issueLabel(node.issue.kind, node.issue.detail)
}

function shouldShowNodeLeaves(svc: Service): boolean {
  const dep = deploymentForService(svc)
  const status = deploymentNodeStatusForService(svc)
  if (!dep || dep.location !== 'remote' || !status) return false
  return status.total > 1 || status.health === 'failed' || status.health === 'warning'
}

function isNodeExpanded(svc: Service): boolean {
  const dep = deploymentForService(svc)
  return !!dep && expandedDeploymentNodes.value.has(dep.id)
}

function toggleNodeExpanded(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep) return
  const next = new Set(expandedDeploymentNodes.value)
  if (next.has(dep.id)) next.delete(dep.id)
  else next.add(dep.id)
  expandedDeploymentNodes.value = next
}

function serviceVersionLabel(svc: Service): string | null {
  const version = svc.version?.trim()
  if (!version) return null
  return version.startsWith('v') ? version : `v${version}`
}

function serviceReplicaLabel(svc: Service): string | null {
  if (typeof svc.replicas !== 'number' || svc.replicas <= 0) return null
  return t('shell.env.replicaCount', { count: svc.replicas })
}

function deploymentMetaLabel(svc: Service): string {
  const dep = deploymentForService(svc)
  const parts = [serviceVersionLabel(svc), serviceReplicaLabel(svc)].filter(Boolean)
  if (parts.length) return parts.join(' · ')
  if (!dep) return ''
  if (dep.location === 'remote') {
    const status = deploymentNodeStatusForService(svc)
    if (status) return deploymentNodeSummary(status)
  }
  const mode = dep.control_mode ?? dep.runtime?.type ?? dep.location
  return t('shell.env.serviceMetaFallback', { location: dep.location, mode })
}

// isServiceOpen 判断本 env 下 service 的 deployment 是否已在某面板打开（用于行高亮）。
function isServiceOpen(svc: Service): boolean {
  const dep = deploymentForService(svc)
  return dep ? props.selectedServiceIds.has(dep.id) : false
}

function canControlDeployment(svc: Service): boolean {
  const dep = deploymentForService(svc)
  return !!dep && dep.read_only !== true && dep.control_mode !== 'monitor'
}

async function startOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  const hostId = singleRemoteHostId(dep)
  if (hostId) {
    await agentStore.startDeploymentOnHost(dep.id, hostId)
    return
  }
  await agentStore.startDeployment(dep.id)
}

async function stopOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  const hostId = singleRemoteHostId(dep)
  if (hostId) {
    await agentStore.stopDeploymentOnHost(dep.id, hostId)
    return
  }
  await agentStore.stopDeployment(dep.id)
}

async function restartOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  const hostId = singleRemoteHostId(dep)
  if (hostId) {
    await agentStore.restartDeploymentOnHost(dep.id, hostId)
    return
  }
  await agentStore.restartDeployment(dep.id)
}

async function startNode(svc: Service, hostId: string) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.startDeploymentOnHost(dep.id, hostId)
}

async function stopNode(svc: Service, hostId: string) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.stopDeploymentOnHost(dep.id, hostId)
}

async function restartNode(svc: Service, hostId: string) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.restartDeploymentOnHost(dep.id, hostId)
}

/**
 * onServiceRowClick 处理 service 行点击事件。
 * 取本 env 下的 deployment，emit open-deployment 打开 deployment 日志面板。
 */
function onServiceRowClick(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep) {
    console.warn('[SuperDev] service has no deployment in this env; cannot open logs', svc.name, props.envName)
    return
  }
  emit('open-deployment', { deploymentId: dep.id, title: `${svc.name} · ${props.envName}` })
}

// ===== env 级批量操作 =====

/**
 * startAll 启动该 env 下所有已选中且未运行的 service 对应的 deployment。
 */
async function startAll() {
  await agentStore.startEnvSelected(props.projectId, props.envName)
}

/**
 * stopAll 停止该 env 下所有运行中的 service 对应的 deployment。
 */
async function stopAll() {
  const deps = props.services
    .filter(svc => canControlDeployment(svc) && isServiceRunningForActions(svc))
    .map(svc => deploymentForService(svc))
    .filter((dep): dep is Deployment => !!dep)
  await Promise.all(deps.map(d => agentStore.stopDeployment(d!.id)))
}

const canStart = computed(() => props.services.some(svc => {
  if (!agentStore.isServiceEnvSelected(props.projectId, props.envName, svc.name)) return false
  return canControlDeployment(svc) && !isServiceRunningForActions(svc)
}))

// ===== 拖拽逻辑 =====

const DRAG_THRESHOLD = 4
const DRAG_NO_SELECT_CLASS = 'service-dragging-no-select'

// 拖拽承载的标识语义为 deploymentId（拖出的面板源是 deployment 单源）。
let draggingDeploymentId: string | null = null
let pendingDeploymentId: string | null = null
let pointerStart: { x: number; y: number } | null = null
let previousUserSelect = ''
let selectionGuardActive = false

function clearTextSelection() {
  window.getSelection()?.removeAllRanges()
}

function beginPointerDrag(deploymentId: string, e: PointerEvent) {
  draggingDeploymentId = deploymentId
  if (!selectionGuardActive) {
    previousUserSelect = document.body.style.userSelect
    document.body.style.userSelect = 'none'
    document.body.classList.add(DRAG_NO_SELECT_CLASS)
    selectionGuardActive = true
  }
  clearTextSelection()
  startServiceDrag(deploymentId, { x: e.clientX, y: e.clientY })
}

function finishPointerDrag() {
  draggingDeploymentId = null
  pendingDeploymentId = null
  pointerStart = null
  if (selectionGuardActive) {
    document.body.style.userSelect = previousUserSelect
    document.body.classList.remove(DRAG_NO_SELECT_CLASS)
    selectionGuardActive = false
  }
}

function onDocumentPointerMove(e: PointerEvent) {
  if (!pointerStart) return
  const dx = Math.abs(e.clientX - pointerStart.x)
  const dy = Math.abs(e.clientY - pointerStart.y)
  if (!draggingDeploymentId && dx < DRAG_THRESHOLD && dy < DRAG_THRESHOLD) return
  e.preventDefault()
  if (!draggingDeploymentId && pendingDeploymentId) {
    beginPointerDrag(pendingDeploymentId, e)
  }
  if (draggingDeploymentId) {
    clearTextSelection()
    moveServiceDrag({ x: e.clientX, y: e.clientY })
  }
}

function onDocumentPointerUp(e: PointerEvent) {
  if (draggingDeploymentId) {
    finishServiceDrag({ x: e.clientX, y: e.clientY })
  }
  finishPointerDrag()
  document.removeEventListener('pointermove', onDocumentPointerMove)
  document.removeEventListener('pointerup', onDocumentPointerUp)
}

// 入参为本 env 下 service 对应的 deploymentId，拖出的面板源即该 deployment。
function onServiceRowPointerDown(svc: Service, e: PointerEvent) {
  if (e.button !== 0) return
  const dep = deploymentForService(svc)
  if (!dep) return
  pendingDeploymentId = dep.id
  pointerStart = { x: e.clientX, y: e.clientY }
  document.addEventListener('pointermove', onDocumentPointerMove)
  document.addEventListener('pointerup', onDocumentPointerUp)
}

onUnmounted(() => {
  document.removeEventListener('pointermove', onDocumentPointerMove)
  document.removeEventListener('pointerup', onDocumentPointerUp)
  endServiceDrag()
})
</script>

<template>
  <div class="env-group">
    <!-- 分组标题行负责环境扫描：名称、数量和低干扰批量操作在同一层。 -->
    <div
      class="env-group-header"
      data-test="env-group-header"
      @click="toggleExpanded"
    >
      <div class="env-title" data-test="env-title">
        <span class="expand-arrow">{{ expanded ? '▾' : '▸' }}</span>
        <span class="env-name">{{ envName }}</span>
        <span class="env-count" data-test="env-service-count">{{ services.length }}</span>
      </div>
      <div class="env-actions" data-test="env-actions" @click.stop>
        <button title="" class="action-btn start" :aria-label="t('shell.env.startAll')" :disabled="!canStart" @click="startAll">▶</button>
        <button title="" class="action-btn search" :aria-label="t('shell.env.searchLogs')" :disabled="services.length === 0" @click="emit('search')">⌕</button>
        <button title="" class="action-btn stop" :aria-label="t('shell.env.stopAll')" @click="stopAll">⏹</button>
      </div>
    </div>

    <!-- 展开后的 service 行列表 -->
    <div v-if="expanded" class="env-group-rows" data-test="env-group-rows">
      <template v-for="svc in services" :key="svc.id">
        <div
          class="env-service-row deployment-card"
          data-test="env-service-row"
          :class="{ selected: isServiceOpen(svc) }"
          @click="onServiceRowClick(svc)"
          @pointerdown="onServiceRowPointerDown(svc, $event)"
        >
          <input
            type="checkbox"
            class="service-checkbox"
            :checked="agentStore.isServiceEnvSelected(projectId, envName, svc.name)"
            :disabled="svc.required"
            @click.stop="onCheckChange(svc)"
          />
          <span
            class="status-dot"
            :style="{ background: serviceStatusColor(svc) }"
          />
          <div class="service-main">
            <div class="service-topline">
              <span class="service-name">{{ svc.name }}</span>
            </div>
            <div class="service-meta" data-test="service-meta">{{ deploymentMetaLabel(svc) }}</div>
          </div>
          <button
            v-if="shouldShowNodeLeaves(svc)"
            type="button"
            class="node-toggle"
            data-test="service-node-toggle"
            :title="t('shell.env.toggleNodes')"
            @click.stop="toggleNodeExpanded(svc)"
            @pointerdown.stop
          >{{ isNodeExpanded(svc) ? '▾' : '▸' }}</button>
          <div
            v-if="showServiceRowActions(svc)"
            class="row-actions"
            data-test="service-action-rail"
            @click.stop
            @pointerdown.stop
          >
            <button
              v-if="!isServiceRunningForActions(svc)"
              type="button"
              class="row-action start"
              data-test="row-start"
              :title="t('shell.env.start')"
              @click="startOne(svc)"
            >▶</button>
            <button
              v-if="isServiceRunningForActions(svc)"
              type="button"
              class="row-action restart"
              data-test="row-restart"
              :title="t('shell.env.restart')"
              @click="restartOne(svc)"
            >↻</button>
            <button
              v-if="isServiceRunningForActions(svc)"
              type="button"
              class="row-action stop"
              data-test="row-stop"
              :title="t('shell.env.stop')"
              @click="stopOne(svc)"
            >⏹</button>
          </div>
        </div>
        <div
          v-if="isNodeExpanded(svc)"
          class="node-leaf-list"
          data-test="env-node-leaf-list"
        >
          <div
            v-for="node in deploymentNodeStatusForService(svc)?.nodes ?? []"
            :key="node.hostId"
            class="node-leaf-row"
            data-test="env-node-leaf-row"
            :title="nodeIssueLabel(node)"
            role="button"
            tabindex="0"
            @click="onServiceRowClick(svc)"
            @keydown.enter.prevent="onServiceRowClick(svc)"
            @keydown.space.prevent="onServiceRowClick(svc)"
          >
            <span class="node-dot" :style="{ background: nodeHealthColor(node.health) }" />
            <span class="node-name">{{ node.hostName }}</span>
            <span class="node-issue">{{ nodeIssueLabel(node) }}</span>
            <div
              v-if="canControlDeployment(svc)"
              class="node-row-actions"
              data-test="node-action-rail"
              @click.stop
              @pointerdown.stop
            >
              <button
                v-if="!isRemoteHostRunningForActions(svc, node.hostId)"
                type="button"
                class="row-action start"
                data-test="node-row-start"
                :title="t('shell.env.start')"
                @click="startNode(svc, node.hostId)"
              >▶</button>
              <button
                v-if="isRemoteHostRunningForActions(svc, node.hostId)"
                type="button"
                class="row-action restart"
                data-test="node-row-restart"
                :title="t('shell.env.restart')"
                @click="restartNode(svc, node.hostId)"
              >↻</button>
              <button
                v-if="isRemoteHostRunningForActions(svc, node.hostId)"
                type="button"
                class="row-action stop"
                data-test="node-row-stop"
                :title="t('shell.env.stop')"
                @click="stopNode(svc, node.hostId)"
              >⏹</button>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.env-group {
  margin: 0 0 8px;
  border: 1px solid rgba(91, 106, 128, 0.22);
  border-radius: 7px;
  overflow: hidden;
  background: rgba(10, 18, 26, 0.38);
}

.env-group-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  padding: 0 8px 0 10px;
  border-radius: 0;
  margin: 0;
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
}

.env-group-header:hover {
  background: rgba(255, 255, 255, 0.035);
}

.env-title {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  gap: 8px;
}

.expand-arrow {
  width: 10px;
  font-size: 11px;
  color: var(--text-secondary);
  flex-shrink: 0;
}

.env-name {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.02em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 0 1 auto;
}

.env-count {
  min-width: 24px;
  height: 20px;
  padding: 0 7px;
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.18);
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 700;
  line-height: 20px;
  text-align: center;
}

.env-actions {
  display: flex;
  gap: 4px;
  align-items: center;
  flex-shrink: 0;
}

.action-btn {
  background: transparent;
  width: 26px;
  height: 26px;
  border: 1px solid rgba(139, 148, 158, 0.2);
  border-radius: 5px;
  padding: 0;
  font-size: 12px;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s;
}
.action-btn:hover:not(:disabled) {
  border-color: rgba(139, 148, 158, 0.34);
  background: rgba(255,255,255,0.08);
}
.action-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.action-btn.start { color: #3fb950; }
.action-btn.search { color: #58a6ff; }
.action-btn.stop { color: var(--text-secondary, #6e7681); }

.env-group-rows {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 0 0 8px;
}

.env-service-row {
  display: grid;
  grid-template-columns: 14px 10px minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 9px;
  min-height: 72px;
  margin: 0;
  padding: 10px 12px 10px 14px;
  border-left: 2px solid transparent;
  border-top: 1px solid rgba(91, 106, 128, 0.12);
  border-radius: 0;
  background: rgba(13, 24, 34, 0.36);
  cursor: pointer;
  color: var(--text-primary, #e6edf3);
  transition: background 0.12s, border-color 0.12s, box-shadow 0.12s;
  user-select: none;
}

.env-service-row:hover {
  background: rgba(18, 32, 46, 0.72);
}

.env-service-row.selected {
  border-left-color: #1f6feb;
  background: rgba(31, 111, 235, 0.14);
  box-shadow: inset 0 0 0 1px rgba(88, 166, 255, 0.1);
}

.service-checkbox {
  width: 12px;
  height: 12px;
  accent-color: #1f6feb;
  flex-shrink: 0;
  cursor: pointer;
}

.status-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
  box-shadow: 0 0 0 3px rgba(63, 185, 80, 0.08);
}

.service-main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.service-topline {
  display: flex;
  align-items: center;
  min-width: 0;
}

.service-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 700;
}

.service-meta {
  color: var(--text-tertiary);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  opacity: 0.88;
  pointer-events: auto;
  flex-shrink: 0;
}

.node-toggle {
  width: 22px;
  height: 22px;
  border: 1px solid rgba(139, 148, 158, 0.2);
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.03);
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 20px;
  cursor: pointer;
}

.node-toggle:hover {
  border-color: rgba(139, 148, 158, 0.36);
  color: var(--text-primary);
}

.node-leaf-list {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 0 8px 4px 44px;
  border-top: 1px solid rgba(91, 106, 128, 0.08);
  background: rgba(7, 15, 22, 0.24);
}

.node-leaf-row {
  display: grid;
  grid-template-columns: 8px minmax(52px, 88px) minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  min-height: 28px;
  padding: 4px 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
}

.node-leaf-row:hover {
  background: rgba(255, 255, 255, 0.045);
  color: var(--text-primary);
}

.node-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
}

.node-name,
.node-issue {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
}

.node-issue {
  color: var(--text-tertiary);
}

.node-row-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  min-width: 52px;
  justify-content: flex-end;
}

.row-action {
  width: 24px;
  height: 24px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary, #8b949e);
  font-size: 13px;
  cursor: pointer;
  line-height: 24px;
  padding: 0;
}

.row-action:hover {
  background: rgba(255, 255, 255, 0.12);
}

.row-action.start {
  color: #3fb950;
}

.row-action.restart {
  color: #d29922;
}

.row-action.stop {
  color: #f85149;
}
</style>
