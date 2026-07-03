import { describe, it, expect, vi } from 'vitest'
import { ScrollIntentMachine } from './logScrollIntent'

describe('ScrollIntentMachine（输入事件驱动）', () => {
  it('初始意图为 follow-bottom', () => {
    const m = new ScrollIntentMachine()
    expect(m.intent).toBe('follow-bottom')
  })

  it('leaveBottom 从 follow-bottom 转 idle', () => {
    const m = new ScrollIntentMachine()
    m.leaveBottom('wheel-up')
    expect(m.intent).toBe('idle')
  })

  it('leaveBottom 从 align-to-time 转 idle，但不打断 anchor-history', () => {
    const m1 = new ScrollIntentMachine({ scrollToLogId: vi.fn() })
    m1.beginTimeAlign('log-1')
    m1.leaveBottom('wheel-up')
    expect(m1.intent).toBe('idle')

    const m2 = new ScrollIntentMachine({ scrollToLogId: vi.fn() })
    m2.beginHistoryAnchor('log-1')
    m2.leaveBottom('wheel-up')
    // 历史回填进行中用户滚动是常态，锚点复位必须完成，否则视口漂移。
    expect(m2.intent).toBe('anchor-history')
  })

  it('maybeReturnToBottom 距底足够近才回 follow 并贴底', () => {
    const scrollToBottom = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom })
    m.leaveBottom('wheel-up')
    m.maybeReturnToBottom({ distanceFromBottom: 200 })
    expect(m.intent).toBe('idle')
    m.maybeReturnToBottom({ distanceFromBottom: 10 })
    expect(m.intent).toBe('follow-bottom')
    expect(scrollToBottom).toHaveBeenCalledTimes(1)
  })

  it('已在 follow 时 maybeReturnToBottom 不重复贴底（避免自激励滚动）', () => {
    const scrollToBottom = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom })
    m.maybeReturnToBottom({ distanceFromBottom: 10 })
    expect(scrollToBottom).not.toHaveBeenCalled()
  })

  it('settleHistoryAnchor 复位后意图落 idle', () => {
    const scrollToLogId = vi.fn()
    const m = new ScrollIntentMachine({ scrollToLogId })
    m.beginHistoryAnchor('log-9')
    m.settleHistoryAnchor()
    expect(scrollToLogId).toHaveBeenCalledWith('log-9')
    expect(m.intent).toBe('idle')
  })

  it('onContentChange 仅 follow 且增长时贴底', () => {
    const scrollToBottom = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom })
    m.onContentChange({ oldCount: 5, newCount: 8 })
    expect(scrollToBottom).toHaveBeenCalledTimes(1)
    m.leaveBottom('wheel-up')
    m.onContentChange({ oldCount: 8, newCount: 12 })
    expect(scrollToBottom).toHaveBeenCalledTimes(1)
  })

  it('jumpToBottom 从任意意图强制回 follow', () => {
    const scrollToBottom = vi.fn()
    const m = new ScrollIntentMachine({ scrollToBottom })
    m.leaveBottom('wheel-up')
    m.jumpToBottom()
    expect(m.intent).toBe('follow-bottom')
    expect(scrollToBottom).toHaveBeenCalled()
  })
})
