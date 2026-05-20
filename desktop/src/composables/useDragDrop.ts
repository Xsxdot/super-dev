// useDragDrop 封装面板拖放逻辑：根据落点位置决定分栏方向。
import { ref } from 'vue'
import type { PanelAxis } from '@/stores/panel'

export type DropEdge = 'left' | 'right' | 'top' | 'bottom' | 'center'
export interface DragPoint {
  x: number
  y: number
}

export interface ServiceDropRequest extends DragPoint {
  id: number
  serviceId: string
}

const draggedServiceId = ref<string | null>(null)
const serviceDragPosition = ref<DragPoint | null>(null)
const serviceDropRequest = ref<ServiceDropRequest | null>(null)
let serviceDropRequestId = 0

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

  function startServiceDrag(serviceId: string, point: DragPoint) {
    draggedServiceId.value = serviceId
    serviceDragPosition.value = point
  }

  function moveServiceDrag(point: DragPoint) {
    if (!draggedServiceId.value) return
    serviceDragPosition.value = point
  }

  function endServiceDrag() {
    draggedServiceId.value = null
    serviceDragPosition.value = null
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
    endServiceDrag()
  }

  return {
    dropHighlight,
    draggedServiceId,
    serviceDragPosition,
    serviceDropRequest,
    getDropEdge,
    edgeToAxis,
    startServiceDrag,
    moveServiceDrag,
    endServiceDrag,
    finishServiceDrag,
  }
}
