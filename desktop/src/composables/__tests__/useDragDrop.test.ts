import { describe, expect, it } from 'vitest'
import { edgeToAxis, getDropEdge, useDragDrop } from '../useDragDrop'

describe('useDragDrop', () => {
  it('getDropEdge 在 20% 边缘和中心区域之间稳定判定', () => {
    expect(getDropEdge({ x: 19, y: 50 }, { w: 100, h: 100 })).toBe('left')
    expect(getDropEdge({ x: 20, y: 50 }, { w: 100, h: 100 })).toBe('center')
    expect(getDropEdge({ x: 80, y: 50 }, { w: 100, h: 100 })).toBe('center')
    expect(getDropEdge({ x: 81, y: 50 }, { w: 100, h: 100 })).toBe('right')
    expect(edgeToAxis('top')).toEqual({ axis: 'v', side: 'first' })
    expect(edgeToAxis('bottom')).toEqual({ axis: 'v', side: 'second' })
  })

  it('支持 deployment source drag payload', () => {
    const dragDrop = useDragDrop() as any

    expect(typeof dragDrop.startSourceDrag).toBe('function')
    expect(typeof dragDrop.finishSourceDrag).toBe('function')

    dragDrop.startSourceDrag({
      type: 'deployment',
      deploymentId: 'dep-1',
    }, { x: 4, y: 8 })
    dragDrop.finishSourceDrag({ x: 10, y: 12 })

    expect(dragDrop.sourceDropRequest.value).toMatchObject({
      source: {
        type: 'deployment',
        deploymentId: 'dep-1',
      },
      x: 10,
      y: 12,
    })
  })
})
