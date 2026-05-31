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
import { ref, computed, onUnmounted } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useDragDrop } from '@/composables/useDragDrop'
import type { Service } from '@/api/agent'

const props = defineProps<{
  envName: string
  isDev: boolean
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
const { startServiceDrag, moveServiceDrag, endServiceDrag, finishServiceDrag } = useDragDrop()

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

// dev 环境默认展开，其他环境默认折叠
const expanded = ref(props.isDev)
const hovered = ref(false)

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

function isRunningStatus(status: string): boolean {
  return status === 'running' || status === 'starting'
}

/**
 * deploymentForService 取出本 env 下 service 对应的 deployment。
 * deployment_id 是系统唯一日志单元，一个 service 在一个 env 下对应一个 deployment。
 */
function deploymentForService(svc: Service) {
  return svc.deployments?.find(d => d.env_name === props.envName)
}

// isServiceOpen 判断本 env 下 service 的 deployment 是否已在某面板打开（用于行高亮）。
function isServiceOpen(svc: Service): boolean {
  const dep = deploymentForService(svc)
  return dep ? props.selectedServiceIds.has(dep.id) : false
}

function canControlDeployment(svc: Service): boolean {
  const dep = deploymentForService(svc)
  return !!dep && dep.read_only !== true
}

async function startOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.startDeployment(dep.id)
}

async function stopOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.stopDeployment(dep.id)
}

async function restartOne(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep || dep.read_only) return
  await agentStore.restartDeployment(dep.id)
}

/**
 * onServiceRowClick 处理 service 行点击事件。
 * 取本 env 下的 deployment，emit open-deployment 打开 deployment 日志面板。
 */
function onServiceRowClick(svc: Service) {
  const dep = deploymentForService(svc)
  if (!dep) {
    console.warn('[SuperDev] service 在该 env 下无 deployment，无法打开日志', svc.name, props.envName)
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
    .map(svc => svc.deployments?.find(d => d.env_name === props.envName))
    .filter(d => d && d.read_only !== true && isRunningStatus(d.status))
  await Promise.all(deps.map(d => agentStore.stopDeployment(d!.id)))
}

const canStart = computed(() => props.services.some(svc => {
  if (!agentStore.isServiceEnvSelected(props.projectId, props.envName, svc.name)) return false
  const dep = svc.deployments?.find(d => d.env_name === props.envName)
  return dep && dep.read_only !== true && !isRunningStatus(dep.status)
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
    <!-- 分组标题行，点击切换折叠/展开，hover 显示操作按钮 -->
    <div
      class="env-group-header"
      data-test="env-group-header"
      @mouseenter="hovered = true"
      @mouseleave="hovered = false"
      @click="toggleExpanded"
    >
      <span class="expand-arrow">{{ expanded ? '▾' : '▸' }}</span>
      <span class="env-name">{{ envName }}</span>
      <Transition name="fade">
        <div v-if="hovered" class="env-actions" @click.stop>
          <button title="启动全部" class="action-btn start" :disabled="!canStart" @click="startAll">▶</button>
          <button title="搜索日志" class="action-btn search" :disabled="services.length === 0" @click="emit('search')">⌕</button>
          <button title="全部停止" class="action-btn stop" @click="stopAll">⏹</button>
        </div>
      </Transition>
    </div>

    <!-- 展开后的 service 行列表 -->
    <div v-if="expanded" class="env-group-rows" data-test="env-group-rows">
      <div
        v-for="svc in services"
        :key="svc.id"
        class="env-service-row"
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
          :style="{
            background: statusColor(
              svc.deployments?.find(d => d.env_name === envName)?.status ?? ''
            ),
          }"
        />
        <span class="service-name">{{ svc.name }}</span>
        <div
          v-if="canControlDeployment(svc)"
          class="row-actions"
          data-test="row-actions"
          @click.stop
          @pointerdown.stop
        >
          <button
            v-if="!isRunningStatus(deploymentForService(svc)?.status ?? '')"
            type="button"
            class="row-action start"
            data-test="row-start"
            title="启动"
            @click="startOne(svc)"
          >▶</button>
          <button
            v-if="isRunningStatus(deploymentForService(svc)?.status ?? '')"
            type="button"
            class="row-action restart"
            data-test="row-restart"
            title="重启"
            @click="restartOne(svc)"
          >↻</button>
          <button
            v-if="isRunningStatus(deploymentForService(svc)?.status ?? '')"
            type="button"
            class="row-action stop"
            data-test="row-stop"
            title="停止"
            @click="stopOne(svc)"
          >⏹</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.env-group {
  margin-bottom: 2px;
}

.env-group-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px 3px 10px;
  border-radius: 4px;
  margin: 1px 4px;
  cursor: pointer;
  transition: background 0.12s;
}

.env-group-header:hover {
  background: rgba(255, 255, 255, 0.04);
}

.expand-arrow {
  font-size: 10px;
  color: var(--text-secondary, #6e7681);
  flex-shrink: 0;
}

.env-name {
  font-size: 11px;
  font-weight: 500;
  color: var(--text-secondary, #6e7681);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
}

.env-actions {
  display: flex;
  gap: 2px;
  align-items: center;
  flex-shrink: 0;
}

.action-btn {
  background: transparent;
  border: none;
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
  cursor: pointer;
  transition: background 0.12s;
}
.action-btn:hover:not(:disabled) { background: rgba(255,255,255,0.08); }
.action-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.action-btn.start { color: #3fb950; }
.action-btn.search { color: #58a6ff; }
.action-btn.stop { color: var(--text-secondary, #6e7681); }

.fade-enter-active, .fade-leave-active { transition: opacity 0.12s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

.env-group-rows {
  margin: 0 4px 2px 4px;
}

.env-service-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px 3px 20px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
  color: var(--text-primary, #e6edf3);
  transition: background 0.12s;
  user-select: none;
}

.env-service-row:hover {
  background: rgba(255, 255, 255, 0.04);
}

.env-service-row.selected {
  background: rgba(31, 111, 235, 0.12);
}

.service-checkbox {
  width: 12px;
  height: 12px;
  accent-color: #1f6feb;
  flex-shrink: 0;
  cursor: pointer;
}

.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.service-name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  opacity: 0;
  transform: translateX(8px);
  transition: opacity 0.14s ease, transform 0.14s ease;
  pointer-events: none;
  flex-shrink: 0;
}

.env-service-row:hover .row-actions {
  opacity: 1;
  transform: translateX(0);
  pointer-events: auto;
}

.row-action {
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-secondary, #8b949e);
  font-size: 11px;
  cursor: pointer;
  line-height: 20px;
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
