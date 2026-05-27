<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, type StyleValue } from 'vue'
import { MAX_PANEL_LEAVES, usePanelStore, projectIdFromPanelSource, type PanelSource } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useDragDrop, type DropEdge } from '@/composables/useDragDrop'
import LogPanel from './LogPanel.vue'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  source?: PanelSource | null
  canClose: boolean
}>()

const panelStore = usePanelStore()
const agentStore = useAgentStore()
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

const source = computed<PanelSource | null>(() =>
  props.source ?? (props.serviceId && props.projectId
    ? { type: 'local-service', serviceId: props.serviceId, projectId: props.projectId }
    : props.projectId
      ? { type: 'local-project', projectId: props.projectId }
      : null)
)

const effectiveProjectId = computed(() =>
  projectIdFromPanelSource(source.value) ?? props.projectId,
)

const service = computed(() =>
  source.value?.type === 'local-service' ? agentStore.serviceById(source.value.serviceId) : null
)

const headerTitle = computed(() => {
  if (service.value) return service.value.name
  if (source.value?.type === 'local-project') {
    const proj = agentStore.projectById(source.value.projectId)
    return proj ? `${proj.name} · 全部` : '未选择'
  }
  if (source.value?.type === 'deployment') return `Deploy: ${source.value.deploymentId}`
  return '未选择'
})

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
  if (value.type === 'local-project') {
    return typeof value.projectId === 'string'
  }
  if (value.type === 'local-service') {
    return typeof value.serviceId === 'string'
      && (value.projectId === undefined || typeof value.projectId === 'string')
  }
  if (value.type === 'deployment') {
    return typeof value.deploymentId === 'string'
  }
  return false
}

function normalizeDropSource(dropSource: PanelSource): PanelSource {
  if (dropSource.type !== 'local-service' || dropSource.projectId) return dropSource
  const svc = agentStore.serviceById(dropSource.serviceId)
  return { ...dropSource, projectId: svc?.project_id ?? '' }
}

function showDropFailure(message: string) {
  window.alert(message)
}

function applySourceDrop(dropSource: PanelSource, edge: DropEdge) {
  const nextSource = normalizeDropSource(dropSource)

  if (edge === 'center') {
    panelStore.replaceSource(props.panelId, nextSource)
    panelStore.setFocus(props.panelId)
  } else {
    const split = edgeToAxis(edge)
    if (split) {
      if (!panelStore.canAddPanelLeaf()) {
        showDropFailure(`已达到最大分栏数（${MAX_PANEL_LEAVES} 个），请先关闭已有分栏后再添加。`)
        return
      }
      panelStore.splitLeafWithSource(props.panelId, split.axis, nextSource, split.side)
    }
  }
}

function applyServiceDrop(serviceId: string, edge: DropEdge) {
  const svc = agentStore.serviceById(serviceId)
  applySourceDrop({ type: 'local-service', serviceId, projectId: svc?.project_id ?? '' }, edge)
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  const rawSource = e.dataTransfer?.getData('application/superdev-panel-source')
  const serviceId = e.dataTransfer?.getData('text/plain')
  const edge = getDropEdgeFromEvent(e) ?? dropHighlight.value
  if (!edge) return
  dropHighlight.value = null
  if (rawSource) {
    const parsedSource = parsePanelSourcePayload(rawSource)
    if (parsedSource) applySourceDrop(parsedSource, edge)
  } else if (serviceId) {
    applyServiceDrop(serviceId, edge)
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
    <div class="panel-header">
      <span class="panel-title">{{ headerTitle }}</span>
      <button v-if="canClose" class="close-btn" @click.stop="panelStore.removeLeaf(panelId)">✕</button>
    </div>

    <!-- Log panel -->
    <LogPanel
      :panel-id="panelId"
      :service-id="source?.type === 'local-service' ? source.serviceId : null"
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
  overflow: hidden;
  min-width: 200px;
}
.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 3px 8px;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
}
.panel-title {
  font-size: 11px;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.close-btn {
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  font-size: 10px;
  cursor: pointer;
  padding: 0 2px;
  line-height: 1;
}
.close-btn:hover { color: var(--text-primary); }

.drop-overlay {
  position: absolute;
  border-radius: 4px;
  background: rgba(31,111,235,0.25);
  border: 2px solid #1f6feb;
  pointer-events: none;
}
</style>
