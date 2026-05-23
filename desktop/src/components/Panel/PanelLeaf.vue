<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, type StyleValue } from 'vue'
import { usePanelStore, projectIdFromPanelSource, type PanelSource } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'
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
const remoteStore = useRemoteStore()
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
  projectIdFromPanelSource(source.value, {
    logSourceById: id => remoteStore.logSourceById(id),
    serviceById: id => agentStore.serviceById(id),
  }) ?? props.projectId,
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
  if (source.value?.type === 'remote-log-source') return `Remote · ${source.value.groupKey}`
  if (source.value?.type === 'remote-aggregate') return `${source.value.serviceName ?? 'Remote'} · ${source.value.groupKey}`
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

function normalizeDropSource(dropSource: PanelSource): PanelSource {
  if (dropSource.type !== 'local-service' || dropSource.projectId) return dropSource
  const svc = agentStore.serviceById(dropSource.serviceId)
  return { ...dropSource, projectId: svc?.project_id ?? '' }
}

function applySourceDrop(dropSource: PanelSource, edge: DropEdge) {
  const nextSource = normalizeDropSource(dropSource)
  if (panelStore.focusEquivalentRemoteSource(nextSource)) return

  if (edge === 'center') {
    panelStore.replaceSource(props.panelId, nextSource)
    panelStore.setFocus(props.panelId)
  } else {
    const split = edgeToAxis(edge)
    if (split) {
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
    applySourceDrop(JSON.parse(rawSource) as PanelSource, edge)
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
      :log-source-id="source?.type === 'remote-log-source' ? source.logSourceId : undefined"
      :log-source-ids="source?.type === 'remote-aggregate' ? source.logSourceIds : undefined"
      :group-key="source?.type === 'remote-log-source' || source?.type === 'remote-aggregate' ? source.groupKey : undefined"
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
