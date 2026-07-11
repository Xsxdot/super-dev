/**
 * 日志虚拟列表与 TanStack Virtual 的集成测试。
 *
 * 职责：
 *   - 使用实际安装的 virtual-core 验证底部锚定会补偿迟到的真实行高
 *   - 覆盖 LogPanel 单元测试中 useVirtualizer mock 无法触达的测量路径
 *
 * 边界：
 *   - 不挂载完整 LogPanel，不建立日志订阅或浏览器布局
 *   - 组件是否传入正确选项由 LogPanel.test.ts 单独约束
 */
import { describe, expect, it, vi } from 'vitest'
import { Virtualizer } from '@tanstack/vue-virtual'

function createVirtualizerHarness(keys: string[], initialOffset = 0) {
  const scrollElement = document.createElement('div')
  scrollElement.scrollTop = initialOffset
  Object.defineProperty(scrollElement, 'clientHeight', { value: 100, configurable: true })
  let virtualizer!: Virtualizer<HTMLElement, HTMLElement>
  Object.defineProperty(scrollElement, 'scrollHeight', {
    configurable: true,
    get: () => virtualizer.getTotalSize(),
  })
  document.body.appendChild(scrollElement)

  let emitOffset: ((offset: number, isScrolling: boolean) => void) | undefined
  const scrollToFn = vi.fn((
    offset: number,
    options: { adjustments?: number; behavior?: ScrollBehavior },
  ) => {
    scrollElement.scrollTop = offset + (options.adjustments ?? 0)
    emitOffset?.(scrollElement.scrollTop, false)
  })
  virtualizer = new Virtualizer<HTMLElement, HTMLElement>({
    count: keys.length,
    getScrollElement: () => scrollElement,
    estimateSize: () => 20,
    getItemKey: index => keys[index],
    anchorTo: 'end',
    followOnAppend: false,
    scrollEndThreshold: 24,
    observeElementRect: (_instance, callback) => {
      callback({ width: 400, height: 100 })
      return undefined
    },
    observeElementOffset: (_instance, callback) => {
      emitOffset = callback
      callback(scrollElement.scrollTop, false)
      return undefined
    },
    scrollToFn,
    measureElement: element => Number(element.dataset.measuredHeight),
  })
  const dispose = virtualizer._didMount()
  virtualizer._willUpdate()

  return {
    scrollElement,
    scrollToFn,
    virtualizer,
    dispose: () => {
      dispose()
      scrollElement.remove()
    },
  }
}

describe('LogPanel virtualizer integration', () => {
  it('keeps an appended row at the DOM end when measureElement reports a taller size', () => {
    const keys = Array.from({ length: 10 }, (_, index) => `row-${index}`)
    const { scrollElement, scrollToFn, virtualizer, dispose } = createVirtualizerHarness(keys)

    try {
      expect(virtualizer.getTotalSize()).toBe(200)
      virtualizer.scrollToEnd()
      expect(virtualizer.getDistanceFromEnd()).toBe(0)

      // 模拟实时 append；是否发起跟随仍由 ScrollIntentMachine 决定，这里只发一次 end 请求。
      const appendedKeys = [...keys, 'row-10']
      virtualizer.setOptions({
        ...virtualizer.options,
        count: appendedKeys.length,
        getItemKey: index => appendedKeys[index],
      })
      virtualizer._willUpdate()
      virtualizer.scrollToEnd()
      expect(virtualizer.getTotalSize()).toBe(220)

      const appendedRow = document.createElement('div')
      appendedRow.dataset.index = '10'
      appendedRow.dataset.measuredHeight = '60'
      scrollElement.appendChild(appendedRow)

      // 新行从 20px 估算值沉降到 60px，真实 measureElement 路径应补偿新增的 40px。
      virtualizer.measureElement(appendedRow)

      expect(virtualizer.getTotalSize()).toBe(260)
      expect(scrollElement.scrollTop).toBe(160)
      expect(virtualizer.getDistanceFromEnd()).toBe(0)
      expect(scrollToFn).toHaveBeenCalledWith(
        120,
        { adjustments: 40, behavior: undefined },
        virtualizer,
      )
    } finally {
      dispose()
    }
  })

  it('preserves the visible anchor when history rows are prepended away from the end', () => {
    const keys = Array.from({ length: 10 }, (_, index) => `row-${index}`)
    const { scrollElement, virtualizer, dispose } = createVirtualizerHarness(keys, 40)

    try {
      expect(virtualizer.getTotalSize()).toBe(200)

      const prependedKeys = ['history-a', 'history-b', ...keys]
      virtualizer.setOptions({
        ...virtualizer.options,
        count: prependedKeys.length,
        getItemKey: index => prependedKeys[index],
      })
      virtualizer._willUpdate()

      // 原来位于顶部的 row-2 前面新增 40px 历史，视口随锚点移动到 80，而不是被拉到列表底部。
      expect(scrollElement.scrollTop).toBe(80)
      expect(virtualizer.getDistanceFromEnd()).toBe(60)
    } finally {
      dispose()
    }
  })

  it('keeps explicit center alignment independent from the end anchor policy', () => {
    const keys = Array.from({ length: 10 }, (_, index) => `row-${index}`)
    const { scrollElement, virtualizer, dispose } = createVirtualizerHarness(keys)

    try {
      virtualizer.getTotalSize()
      virtualizer.scrollToIndex(5, { align: 'center' })

      expect(scrollElement.scrollTop).toBe(60)
      expect(virtualizer.getDistanceFromEnd()).toBe(40)
    } finally {
      dispose()
    }
  })
})
