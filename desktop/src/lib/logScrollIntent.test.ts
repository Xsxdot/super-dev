import { describe, it, expect, vi } from 'vitest'
import { ScrollIntentMachine } from './logScrollIntent'

describe('ScrollIntentMachine', () => {
  it('默认意图为 follow-bottom', () => {
    const m = new ScrollIntentMachine()
    expect(m.intent).toBe('follow-bottom')
  })

  it('用户上滚进入 idle，到底部回 follow-bottom', () => {
    const m = new ScrollIntentMachine()
    m.onUserScroll({ distanceFromBottom: 200 })
    expect(m.intent).toBe('idle')
    m.onUserScroll({ distanceFromBottom: 0 })
    expect(m.intent).toBe('follow-bottom')
  })

  it('follow-bottom 仅在内容增长时请求一次滚动', () => {
    const scrollTo = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom: scrollTo })
    m.onContentChange({ oldCount: 10, newCount: 12 })
    expect(scrollTo).toHaveBeenCalledTimes(1)
    m.onContentChange({ oldCount: 12, newCount: 12 })
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  it('idle 下内容增长不滚动', () => {
    const scrollTo = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom: scrollTo })
    m.onUserScroll({ distanceFromBottom: 200 })
    m.onContentChange({ oldCount: 10, newCount: 30 })
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('beginHistoryAnchor 记锚点，settle 用稳定 id 复位', () => {
    const scrollToId = vi.fn()
    const m = new ScrollIntentMachine({ scrollToLogId: scrollToId })
    m.beginHistoryAnchor('log-42')
    expect(m.intent).toBe('anchor-history')
    m.settleHistoryAnchor()
    expect(scrollToId).toHaveBeenCalledWith('log-42')
  })

  it('beginTimeAlign 暂停 follow 并对齐', () => {
    const scrollToId = vi.fn()
    const m = new ScrollIntentMachine({ scrollToLogId: scrollToId })
    m.beginTimeAlign('log-7')
    expect(m.intent).toBe('align-to-time')
    expect(scrollToId).toHaveBeenCalledWith('log-7')
    m.onContentChange({ oldCount: 5, newCount: 9 })
    expect(m.intent).toBe('align-to-time')
  })

  it('时间同步单向：被动方对齐不回调主动方（无回环）', () => {
    const broadcast = vi.fn()
    const m = new ScrollIntentMachine({ broadcastCursor: broadcast })
    m.beginTimeAlign('log-7')
    expect(broadcast).not.toHaveBeenCalled()
  })

  it('jumpToBottom 强制回 follow-bottom 并滚动', () => {
    const scrollTo = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom: scrollTo })
    m.beginTimeAlign('log-7')
    m.jumpToBottom()
    expect(m.intent).toBe('follow-bottom')
    expect(scrollTo).toHaveBeenCalled()
  })

  it('过滤重建：follow-bottom 贴底，idle 保持不滚', () => {
    const scrollTo = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom: scrollTo })
    m.onFilterRebuild({ oldCount: 100, newCount: 20 })
    expect(scrollTo).toHaveBeenCalledTimes(1)
    m.onUserScroll({ distanceFromBottom: 300 })
    scrollTo.mockClear()
    m.onFilterRebuild({ oldCount: 20, newCount: 8 })
    expect(scrollTo).not.toHaveBeenCalled()
  })
})
