import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { installFrontendDiagnosticsBridge, FLUSH_INTERVAL_MS } from '../frontendDiagnostics'
import { api } from '@/api/agent'

vi.mock('@/api/agent', () => ({
  api: { postFrontendDiagnostics: vi.fn().mockResolvedValue({ accepted: 1 }) },
}))

describe('frontendDiagnostics bridge', () => {
  let uninstall: () => void

  beforeEach(() => {
    vi.useFakeTimers()
    uninstall = installFrontendDiagnosticsBridge()
  })

  afterEach(() => {
    uninstall()
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('收集 log-panel 事件并按周期批量上报', async () => {
    window.dispatchEvent(new CustomEvent('superdev:log-panel', {
      detail: { scope: 'log-panel', level: 'debug', event: 'scroll_intent.transition', at: '2026-07-03T04:00:00Z' },
    }))
    window.dispatchEvent(new CustomEvent('superdev:log-evidence', {
      detail: { scope: 'log-evidence', level: 'info', event: 'pin.note.open', at: '2026-07-03T04:00:01Z' },
    }))

    await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS)

    expect(api.postFrontendDiagnostics).toHaveBeenCalledTimes(1)
    const events = vi.mocked(api.postFrontendDiagnostics).mock.calls[0][0]
    expect(events).toHaveLength(2)
    expect(events[0].event).toBe('scroll_intent.transition')
  })

  it('收集 onboarding 结构化事件并保留上下文', async () => {
    window.dispatchEvent(new CustomEvent('superdev:onboarding', {
      detail: {
        scope: 'onboarding',
        level: 'info',
        event: 'agents.detect.succeeded',
        at: '2026-07-10T08:00:00Z',
        detectedCount: 2,
      },
    }))

    await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS)

    expect(api.postFrontendDiagnostics).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.postFrontendDiagnostics).mock.calls[0][0]).toEqual([
      expect.objectContaining({
        scope: 'onboarding',
        event: 'agents.detect.succeeded',
        detectedCount: 2,
      }),
    ])
  })

  it('收集 Windows 自绘标题栏的窗口动作事件', async () => {
    window.dispatchEvent(new CustomEvent('superdev:shell', {
      detail: {
        scope: 'shell',
        level: 'error',
        event: 'titlebar.action.failed',
        at: '2026-07-13T08:00:00Z',
        action: 'minimize',
      },
    }))

    await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS)

    expect(vi.mocked(api.postFrontendDiagnostics).mock.calls[0][0]).toEqual([
      expect.objectContaining({
        scope: 'shell',
        event: 'titlebar.action.failed',
        action: 'minimize',
      }),
    ])
  })

  it('上报失败时保留队列下轮重试，且队列有上限', async () => {
    vi.mocked(api.postFrontendDiagnostics).mockRejectedValueOnce(new Error('down'))
    window.dispatchEvent(new CustomEvent('superdev:log-panel', {
      detail: { scope: 'log-panel', level: 'debug', event: 'e1', at: '2026-07-03T04:00:00Z' },
    }))

    await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS)
    await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS)

    expect(api.postFrontendDiagnostics).toHaveBeenCalledTimes(2)
    expect(vi.mocked(api.postFrontendDiagnostics).mock.calls[1][0]).toHaveLength(1)
  })
})
