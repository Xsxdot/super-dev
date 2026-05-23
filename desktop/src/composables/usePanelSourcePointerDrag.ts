import { onUnmounted, ref } from 'vue'
import { useDragDrop } from '@/composables/useDragDrop'
import type { PanelSource } from '@/stores/panel'

const DRAG_THRESHOLD = 4
const DRAG_NO_SELECT_CLASS = 'service-dragging-no-select'

export function usePanelSourcePointerDrag(sourceFactory: () => PanelSource) {
  const { startSourceDrag, moveSourceDrag, endSourceDrag, finishSourceDrag } = useDragDrop()
  const dragging = ref(false)

  let pointerStart: { x: number; y: number } | null = null
  let suppressClick = false
  let previousUserSelect = ''
  let selectionGuardActive = false

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
    startSourceDrag(sourceFactory(), { x: e.clientX, y: e.clientY })
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
    moveSourceDrag({ x: e.clientX, y: e.clientY })
  }

  function onDocumentPointerUp(e: PointerEvent) {
    if (dragging.value) {
      finishSourceDrag({ x: e.clientX, y: e.clientY })
    }
    finishPointerDrag()
    stopPointerListeners()
  }

  function consumeClickSuppression(): boolean {
    if (!suppressClick) return false
    suppressClick = false
    return true
  }

  onUnmounted(() => {
    finishPointerDrag()
    stopPointerListeners()
    endSourceDrag()
  })

  return {
    dragging,
    onPointerDown,
    consumeClickSuppression,
  }
}
