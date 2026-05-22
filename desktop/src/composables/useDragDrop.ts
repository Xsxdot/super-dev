// useDragDrop 封装面板拖放逻辑：根据落点位置决定分栏方向。
import { ref } from 'vue'
import type { PanelAxis, PanelSource } from '@/stores/panel'

export type DropEdge = 'left' | 'right' | 'top' | 'bottom' | 'center'
export interface DragPoint {
  x: number
  y: number
}

export interface ServiceDropRequest extends DragPoint {
  id: number
  serviceId: string
}

export interface SourceDropRequest extends DragPoint {
  id: number
  source: PanelSource
}

const draggedSource = ref<PanelSource | null>(null)
const sourceDragPosition = ref<DragPoint | null>(null)
const sourceDropRequest = ref<SourceDropRequest | null>(null)
const draggedServiceId = ref<string | null>(null)
const serviceDragPosition = ref<DragPoint | null>(null)
const serviceDropRequest = ref<ServiceDropRequest | null>(null)
let serviceDropRequestId = 0
let sourceDropRequestId = 0

export function getDropEdge(location: { x: number; y: number }, size: { w: number; h: number }): DropEdge {
  const { x, y } = location
  const { w, h } = size
  if (w <= 0 || h <= 0) return 'center'

  const innerW = w * 0.6
  const innerH = h * 0.6
  const innerLeft = (w - innerW) / 2
  const innerTop = (h - innerH) / 2

  if (x >= innerLeft && x <= innerLeft + innerW && y >= innerTop && y <= innerTop + innerH) {
    return 'center'
  }

  const edgeFraction = 0.2
  if (x < w * edgeFraction) return 'left'
  if (x > w * (1 - edgeFraction)) return 'right'
  if (y < h * edgeFraction) return 'top'
  if (y > h * (1 - edgeFraction)) return 'bottom'
  return 'center'
}

export function edgeToAxis(edge: DropEdge): { axis: PanelAxis; side: 'first' | 'second' } | null {
  if (edge === 'left') return { axis: 'h', side: 'first' }
  if (edge === 'right') return { axis: 'h', side: 'second' }
  if (edge === 'top') return { axis: 'v', side: 'first' }
  if (edge === 'bottom') return { axis: 'v', side: 'second' }
  return null
}

export function useDragDrop() {
  const dropHighlight = ref<DropEdge | null>(null)

  function startSourceDrag(source: PanelSource, point: DragPoint) {
    draggedSource.value = source
    sourceDragPosition.value = point
  }

  function startServiceDrag(serviceId: string, point: DragPoint) {
    draggedServiceId.value = serviceId
    serviceDragPosition.value = point
  }

  function moveSourceDrag(point: DragPoint) {
    if (!draggedSource.value) return
    sourceDragPosition.value = point
  }

  function moveServiceDrag(point: DragPoint) {
    if (!draggedServiceId.value) return
    serviceDragPosition.value = point
  }

  function endSourceDrag() {
    draggedSource.value = null
    sourceDragPosition.value = null
  }

  function endServiceDrag() {
    draggedServiceId.value = null
    serviceDragPosition.value = null
  }

  function finishSourceDrag(point: DragPoint) {
    const source = draggedSource.value
    if (source) {
      sourceDropRequest.value = {
        id: ++sourceDropRequestId,
        source,
        ...point,
      }
    }
    endSourceDrag()
  }

  function finishServiceDrag(point: DragPoint) {
    const serviceId = draggedServiceId.value
    if (serviceId) {
      serviceDropRequest.value = {
        id: ++serviceDropRequestId,
        serviceId,
        ...point,
      }
    }
    draggedServiceId.value = null
    serviceDragPosition.value = null
  }

  return {
    dropHighlight,
    draggedSource,
    sourceDragPosition,
    sourceDropRequest,
    draggedServiceId,
    serviceDragPosition,
    serviceDropRequest,
    getDropEdge,
    edgeToAxis,
    startSourceDrag,
    moveSourceDrag,
    endSourceDrag,
    finishSourceDrag,
    startServiceDrag,
    moveServiceDrag,
    endServiceDrag,
    finishServiceDrag,
  }
}
