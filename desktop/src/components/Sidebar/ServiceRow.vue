<script setup lang="ts">
import { ref, computed, onUnmounted } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useDragDrop } from '@/composables/useDragDrop'
import type { Service } from '@/api/agent'

const props = defineProps<{
  service: Service
  projectId: string
  selected: boolean
}>()

const emit = defineEmits<{
  click: []
  'open-deployment': [payload: { deploymentId: string; title: string }]
}>()

const agentStore = useAgentStore()
const { startServiceDrag, moveServiceDrag, endServiceDrag, finishServiceDrag } = useDragDrop()
const hovered = ref(false)
const dragging = ref(false)
// 有 deployments 时默认展开子行列表
const expanded = ref(true)

const hasDeployments = computed(() => Boolean(props.service.deployments?.length))

let pointerStart: { x: number; y: number } | null = null
let suppressClick = false
let previousUserSelect = ''
let selectionGuardActive = false

const DRAG_THRESHOLD = 4
const DRAG_NO_SELECT_CLASS = 'service-dragging-no-select'

const isChecked = computed(() =>
  agentStore.isServiceSelectedForStart(props.projectId, props.service.name)
)

async function onCheckChange() {
  if (props.service.required) return
  const project = agentStore.projectById(props.projectId)
  if (!project) return
  const current = project.selected_service_ids ?? []
  const next = isChecked.value
    ? current.filter(n => n !== props.service.name)
    : [...current, props.service.name]
  await agentStore.updateSelected(props.projectId, next)
}

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

function isInteractiveTarget(target: EventTarget | null): boolean {
  return target instanceof Element && Boolean(target.closest('input, button, a, textarea, select'))
}

function startPointerListeners() {
  document.addEventListener('pointermove', onDocumentPointerMove)
  document.addEventListener('pointerup', onDocumentPointerUp)
}

function stopPointerListeners() {
  document.removeEventListener('pointermove', onDocumentPointerMove)
  document.removeEventListener('pointerup', onDocumentPointerUp)
}

function clearTextSelection() {
  window.getSelection()?.removeAllRanges()
}

function beginPointerDrag(e: PointerEvent) {
  dragging.value = true
  suppressClick = true
  if (!selectionGuardActive) {
    previousUserSelect = document.body.style.userSelect
    document.body.style.userSelect = 'none'
    document.body.classList.add(DRAG_NO_SELECT_CLASS)
    selectionGuardActive = true
  }
  clearTextSelection()
  startServiceDrag(props.service.id, { x: e.clientX, y: e.clientY })
}

function finishPointerDrag() {
  dragging.value = false
  pointerStart = null
  if (selectionGuardActive) {
    document.body.style.userSelect = previousUserSelect
    document.body.classList.remove(DRAG_NO_SELECT_CLASS)
    selectionGuardActive = false
  }
}

function onPointerDown(e: PointerEvent) {
  if (e.button !== 0 || isInteractiveTarget(e.target)) return
  pointerStart = { x: e.clientX, y: e.clientY }
  startPointerListeners()
}

function onDocumentPointerMove(e: PointerEvent) {
  if (!pointerStart) return
  const dx = Math.abs(e.clientX - pointerStart.x)
  const dy = Math.abs(e.clientY - pointerStart.y)
  if (!dragging.value && dx < DRAG_THRESHOLD && dy < DRAG_THRESHOLD) return
  e.preventDefault()
  if (!dragging.value) beginPointerDrag(e)
  clearTextSelection()
  moveServiceDrag({ x: e.clientX, y: e.clientY })
}

function onDocumentPointerUp(e: PointerEvent) {
  if (dragging.value) {
    finishServiceDrag({ x: e.clientX, y: e.clientY })
  }
  finishPointerDrag()
  stopPointerListeners()
}

function onClick() {
  if (suppressClick) {
    suppressClick = false
    return
  }
  // 有 deployments 时切换折叠/展开，不 emit click
  if (hasDeployments.value) {
    expanded.value = !expanded.value
    return
  }
  emit('click')
}

function onDeploymentClick(dep: NonNullable<Service['deployments']>[number]) {
  emit('open-deployment', {
    deploymentId: dep.id,
    title: `${props.service.name} · ${dep.env_name}`,
  })
}

onUnmounted(() => {
  finishPointerDrag()
  stopPointerListeners()
  endServiceDrag()
})
</script>

<template>
  <div class="service-row-wrapper">
    <div
      class="service-row"
      :class="{ selected, dragging }"
      @mouseenter="hovered = true"
      @mouseleave="hovered = false"
      @click="onClick"
      @pointerdown="onPointerDown"
    >
      <input
        type="checkbox"
        :checked="isChecked"
        :disabled="service.required"
        @click.stop="onCheckChange"
        class="service-checkbox"
      />
      <span class="status-dot" :style="{ background: statusColor(service.status) }" />
      <span class="service-name">{{ service.name }}</span>

      <!-- 有 deployments 时显示折叠/展开箭头，替代 hover 启停按钮 -->
      <span v-if="hasDeployments" class="expand-arrow">
        {{ expanded ? '▾' : '▸' }}
      </span>

      <!-- 无 deployments 时保留 hover 启停按钮 -->
      <Transition v-else name="fade">
        <div v-if="hovered" class="hover-actions" @click.stop>
          <template v-if="service.status === 'running' || service.status === 'starting'">
            <button title="重启" @click="agentStore.restartService(service.id)">↺</button>
            <button title="停止" class="stop-btn" @click="agentStore.stopService(service.id)">⏹</button>
          </template>
          <template v-else>
            <button title="启动" class="start-btn" @click="agentStore.startService(service.id)">▶</button>
          </template>
        </div>
      </Transition>
    </div>

    <!-- Deployment 子行列表 -->
    <div
      v-if="hasDeployments && expanded"
      class="deployment-list"
      data-test="deployment-list"
    >
      <div
        v-for="dep in service.deployments"
        :key="dep.id"
        class="deployment-row"
        data-test="deployment-row"
        @click="onDeploymentClick(dep)"
      >
        <span class="deployment-location-icon">{{ dep.location === 'remote' ? '☁' : '⬡' }}</span>
        <span class="deployment-env-name">{{ dep.env_name }}</span>
        <span class="deployment-status-dot" :style="{ background: statusColor(dep.status) }" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.service-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px 3px 10px;
  border-radius: 4px;
  margin: 1px 4px;
  cursor: pointer;
  transition: background 0.12s;
}
.service-row:hover { background: rgba(255,255,255,0.04); }
.service-row.selected { background: rgba(31,111,235,0.12); }
.service-row.dragging { opacity: 0.7; cursor: grabbing; }

.service-checkbox {
  width: 12px; height: 12px;
  accent-color: #1f6feb;
  flex-shrink: 0;
  cursor: pointer;
}

.status-dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.service-name {
  flex: 1;
  font-size: 12px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hover-actions {
  display: flex;
  gap: 3px;
  align-items: center;
  background: linear-gradient(to right, transparent, var(--bg-elevated) 40%);
  padding-left: 16px;
  position: absolute;
  right: 6px;
}
.hover-actions button {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 3px;
  padding: 1px 5px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
}
.hover-actions .stop-btn {
  background: rgba(248,81,73,0.1);
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}
.hover-actions .start-btn {
  background: rgba(63,185,80,0.1);
  border-color: rgba(63,185,80,0.3);
  color: #3fb950;
}

.fade-enter-active, .fade-leave-active { transition: opacity 0.15s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

.expand-arrow {
  font-size: 10px;
  color: var(--text-secondary);
  flex-shrink: 0;
}

.deployment-list {
  margin: 0 4px 2px 4px;
}

.deployment-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px 2px 28px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 11px;
  color: var(--text-secondary);
  transition: background 0.12s;
}
.deployment-row:hover { background: rgba(255,255,255,0.04); }

.deployment-location-icon {
  font-size: 10px;
  flex-shrink: 0;
}

.deployment-env-name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.deployment-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}
</style>
