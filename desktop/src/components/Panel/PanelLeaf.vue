<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, type StyleValue } from 'vue'
import { MAX_PANEL_LEAVES, usePanelStore, type PanelAxis, type PanelSource } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useDragDrop, type DropEdge } from '@/composables/useDragDrop'
import { useAppI18n } from '@/i18n/useAppI18n'
import LogPanel from './LogPanel.vue'

const props = defineProps<{
  panelId: string
  serviceId?: string | null
  projectId?: string | null
  source?: PanelSource | null
  canClose: boolean
}>()

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const { t } = useAppI18n()
const {
  dropHighlight,
  draggedSource,
  sourceDragPosition,
  sourceDropRequest,
  draggedServiceId,
  serviceDragPosition,
  serviceDropRequest,
  getDropEdge,
  edgeToAxis,
} = useDragDrop()

const panelEl = ref<HTMLElement | null>(null)
const isFocused = computed(() => panelStore.focusedPanelId === props.panelId)

// deployment 单源：来源即 props.source，不再从 serviceId/projectId 兜底构造。
const source = computed<PanelSource | null>(() => props.source ?? null)

// 同一 leaf 可通过中心拖放替换 deployment；source 身份变化时必须重建 LogPanel，
// 防止旧 deployment 的显示缓存、滚动意图和 virtualizer 行高缓存继续存活。
const logPanelIdentity = computed(() => {
  const deploymentId = source.value?.type === 'deployment' ? source.value.deploymentId : 'empty'
  return `${props.panelId}:${deploymentId}`
})

// deployment 反查所属 service + env，供面板标题与项目规则加载使用。
const deploymentInfo = computed(() =>
  source.value?.type === 'deployment'
    ? agentStore.serviceForDeployment(source.value.deploymentId)
    : undefined,
)

// 项目规则需要 projectId：通过 deployment 反查 service 所属项目。
const effectiveProjectId = computed(() =>
  deploymentInfo.value?.service.project_id ?? null,
)

const headerTitle = computed(() => {
  if (source.value?.type === 'deployment') {
    const info = deploymentInfo.value
    if (info) return `${info.service.name} · ${info.envName}`
    // 反查不到（数据尚未加载或已删除）时退回截断的 deployment id
    return t('panel.deploymentFallbackTitle', { id: source.value.deploymentId.slice(0, 12) })
  }
  return t('panel.emptyTitle')
})

const deploymentStatus = computed(() =>
  deploymentInfo.value?.deployment.status ?? '',
)

const isLiveDeployment = computed(() =>
  deploymentStatus.value === 'running' || deploymentStatus.value === 'starting',
)

const statusColor = computed(() => {
  if (deploymentStatus.value === 'running') return '#3fb950'
  if (deploymentStatus.value === 'starting') return '#d29922'
  if (deploymentStatus.value === 'failed') return '#f85149'
  return '#6e7681'
})

const serviceName = computed(() => deploymentInfo.value?.service.name ?? headerTitle.value)
const envName = computed(() => deploymentInfo.value?.envName ?? '')

function onDragOver(e: DragEvent) {
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
  dropHighlight.value = getDropEdgeFromEvent(e)
}

function isInsidePanel(e: DragEvent): boolean {
  if (!panelEl.value) return false
  const rect = panelEl.value.getBoundingClientRect()
  return e.clientX >= rect.left
    && e.clientX <= rect.right
    && e.clientY >= rect.top
    && e.clientY <= rect.bottom
}

function onDragLeave(e: DragEvent) {
  if (isInsidePanel(e)) return
  dropHighlight.value = null
}

function getDropEdgeFromEvent(e: DragEvent): DropEdge | null {
  return getDropEdgeAt(e.clientX, e.clientY)
}

function getDropEdgeAt(clientX: number, clientY: number): DropEdge | null {
  if (!panelEl.value) return null
  const rect = panelEl.value.getBoundingClientRect()
  if (
    clientX < rect.left
    || clientX > rect.right
    || clientY < rect.top
    || clientY > rect.bottom
  ) {
    return null
  }
  return getDropEdge(
    { x: clientX - rect.left, y: clientY - rect.top },
    { w: rect.width, h: rect.height }
  )
}

function parsePanelSourcePayload(rawSource: string): PanelSource | null {
  try {
    const parsed = JSON.parse(rawSource) as unknown
    return isSupportedPanelSource(parsed) ? parsed : null
  } catch {
    return null
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isSupportedPanelSource(value: unknown): value is PanelSource {
  if (!isRecord(value) || typeof value.type !== 'string') return false
  if (value.type === 'deployment') {
    return typeof value.deploymentId === 'string'
  }
  return false
}

function showDropFailure(message: string) {
  window.alert(message)
}

function splitEmptyPanel(axis: PanelAxis) {
  if (!panelStore.canAddPanelLeaf()) {
    showDropFailure(t('panel.maxLeavesAlert', { count: MAX_PANEL_LEAVES }))
    return
  }
  panelStore.splitLeafWithSource(props.panelId, axis, null, 'second')
}

function applySourceDrop(nextSource: PanelSource, edge: DropEdge) {
  if (edge === 'center') {
    panelStore.replaceSource(props.panelId, nextSource)
    panelStore.setFocus(props.panelId)
  } else {
    const split = edgeToAxis(edge)
    if (split) {
      if (!panelStore.canAddPanelLeaf()) {
        showDropFailure(t('panel.maxLeavesAlert', { count: MAX_PANEL_LEAVES }))
        return
      }
      panelStore.splitLeafWithSource(props.panelId, split.axis, nextSource, split.side)
    }
  }
}

// 侧边栏拖拽 text/plain 现在承载的是 deploymentId（见 EnvGroup），据此构造 deployment 来源。
function applyServiceDrop(deploymentId: string, edge: DropEdge) {
  applySourceDrop({ type: 'deployment', deploymentId }, edge)
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  const rawSource = e.dataTransfer?.getData('application/superdev-panel-source')
  const deploymentId = e.dataTransfer?.getData('text/plain')
  const edge = getDropEdgeFromEvent(e) ?? dropHighlight.value
  if (!edge) return
  dropHighlight.value = null
  if (rawSource) {
    const parsedSource = parsePanelSourcePayload(rawSource)
    if (parsedSource) applySourceDrop(parsedSource, edge)
  } else if (deploymentId) {
    applyServiceDrop(deploymentId, edge)
  }
}

function onDocumentPointerMove(e: PointerEvent) {
  if (!draggedSource.value && !draggedServiceId.value) return
  const edge = getDropEdgeAt(e.clientX, e.clientY)
  dropHighlight.value = edge
}

function highlightStyle(edge: DropEdge | null): StyleValue {
  if (!edge) return {}
  const styles: Record<DropEdge, StyleValue> = {
    left:   { left: 0, top: 0, width: '20%', height: '100%' },
    right:  { right: 0, top: 0, width: '20%', height: '100%' },
    top:    { left: 0, top: 0, width: '100%', height: '20%' },
    bottom: { left: 0, bottom: 0, width: '100%', height: '20%' },
    center: { left: '20%', top: '20%', width: '60%', height: '60%' },
  }
  return styles[edge]
}

onMounted(() => {
  document.addEventListener('pointermove', onDocumentPointerMove)
})

onUnmounted(() => {
  document.removeEventListener('pointermove', onDocumentPointerMove)
})

watch(sourceDragPosition, (point) => {
  if (!draggedSource.value || !point) return
  dropHighlight.value = getDropEdgeAt(point.x, point.y)
})

watch(serviceDragPosition, (point) => {
  if (!draggedServiceId.value || !point) return
  dropHighlight.value = getDropEdgeAt(point.x, point.y)
})

watch(sourceDropRequest, (request) => {
  if (!request) return
  const edge = getDropEdgeAt(request.x, request.y)
  dropHighlight.value = null
  if (edge) {
    applySourceDrop(request.source, edge)
  }
})

watch(serviceDropRequest, (request) => {
  if (!request) return
  const edge = getDropEdgeAt(request.x, request.y)
  dropHighlight.value = null
  if (edge) {
    applyServiceDrop(request.serviceId, edge)
  }
})
</script>

<template>
  <div
    ref="panelEl"
    class="panel-leaf"
    :class="{ focused: isFocused }"
    @click="panelStore.setFocus(panelId)"
    @dragenter="onDragOver"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <!-- Panel header -->
    <div class="panel-header" data-test="panel-card-header">
      <div class="panel-identity">
        <span class="panel-status-dot" :style="{ background: statusColor }" />
        <div class="panel-title-stack">
          <div class="panel-title-line">
            <span class="panel-title" data-test="panel-service-name">{{ serviceName }}</span>
            <span v-if="envName" class="panel-env" data-test="panel-env-name">· {{ envName }}</span>
          </div>
          <div v-if="source" class="panel-live-line" data-test="panel-live-state">
            <span :class="{ active: isLiveDeployment }">{{ t('panel.state.live') }}</span>
            <span>·</span>
            <span>{{ t('panel.state.following') }}</span>
          </div>
          <div v-else class="panel-live-line empty">{{ t('panel.state.empty') }}</div>
        </div>
      </div>
      <div class="panel-actions">
        <button
          type="button"
          class="panel-action-btn"
          data-test="panel-split-right"
          :aria-label="t('panel.actions.splitRight')"
          :title="t('panel.actions.splitRight')"
          @click.stop="splitEmptyPanel('h')"
        >
          <span class="split-icon split-right-icon" aria-hidden="true" />
        </button>
        <button
          type="button"
          class="panel-action-btn"
          data-test="panel-split-down"
          :aria-label="t('panel.actions.splitDown')"
          :title="t('panel.actions.splitDown')"
          @click.stop="splitEmptyPanel('v')"
        >
          <span class="split-icon split-down-icon" aria-hidden="true" />
        </button>
        <button
          v-if="canClose"
          type="button"
          class="panel-action-btn close-btn"
          :aria-label="t('panel.actions.close')"
          :title="t('panel.actions.close')"
          @click.stop="panelStore.removeLeaf(panelId)"
        >
          ×
        </button>
      </div>
    </div>

    <!-- Log panel -->
    <LogPanel
      :key="logPanelIdentity"
      :panel-id="panelId"
      :project-id="effectiveProjectId"
      :source="source"
    />

    <!-- Drop highlight overlay -->
    <div
      v-if="dropHighlight"
      class="drop-overlay"
      :style="highlightStyle(dropHighlight)"
    />
  </div>
</template>

<style scoped>
.panel-leaf {
  position: relative;
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 260px;
  min-height: 0;
  overflow: hidden;
  border: 1px solid rgba(139, 148, 158, 0.22);
  border-radius: 8px;
  background: rgba(9, 20, 28, 0.92);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.035);
}
.panel-leaf.focused {
  border-color: rgba(88, 166, 255, 0.42);
}
.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-height: 48px;
  padding: 8px 10px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.18);
  background: rgba(255, 255, 255, 0.025);
  flex-shrink: 0;
}
.panel-identity {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}
.panel-status-dot {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  flex-shrink: 0;
}
.panel-title-stack {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.panel-title-line {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.panel-title {
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.panel-env {
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
}
.panel-live-line {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1;
}
.panel-live-line .active {
  color: #7ce38b;
}
.panel-live-line.empty {
  color: var(--text-tertiary);
}
.panel-actions {
  display: flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
}
.panel-action-btn {
  position: relative;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  color: var(--text-secondary);
  cursor: pointer;
  line-height: 1;
}
.panel-action-btn:hover {
  border-color: rgba(139, 148, 158, 0.26);
  background: rgba(255, 255, 255, 0.055);
  color: var(--text-primary);
}
.split-icon {
  position: relative;
  width: 13px;
  height: 11px;
  border: 1px solid currentColor;
  border-radius: 2px;
}
.split-icon::after {
  position: absolute;
  content: '';
  background: currentColor;
}
.split-right-icon::after {
  top: 0;
  bottom: 0;
  left: 6px;
  width: 1px;
}
.split-down-icon::after {
  right: 0;
  bottom: 5px;
  left: 0;
  height: 1px;
}
.close-btn {
  font-size: 13px;
}

.drop-overlay {
  position: absolute;
  border-radius: 4px;
  background: rgba(31,111,235,0.25);
  border: 2px solid #1f6feb;
  pointer-events: none;
}
</style>
