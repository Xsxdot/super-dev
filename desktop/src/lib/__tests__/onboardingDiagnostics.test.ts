/**
 * onboardingDiagnostics 测试首次启动结构化诊断事件。
 *
 * 职责：
 *   - 验证事件契约、时间戳和调用方上下文不被修改
 *
 * 边界：
 *   - 不启动诊断桥，也不调用真实 agent API
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { emitOnboardingDiagnostic } from '../onboardingDiagnostics'

describe('emitOnboardingDiagnostic', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-10T08:30:00.000Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
    window.history.replaceState({}, '', '/')
  })

  it('派发带统一 scope、时间戳和上下文的事件', () => {
    const context = { detectedCount: 2, detectedAgents: ['claude-code', 'codex'] }
    const listener = vi.fn()
    window.addEventListener('superdev:onboarding', listener, { once: true })

    emitOnboardingDiagnostic('agents.detect.succeeded', 'info', context)

    expect(listener).toHaveBeenCalledTimes(1)
    expect((listener.mock.calls[0][0] as CustomEvent).detail).toEqual({
      scope: 'onboarding',
      mode: 'runtime',
      level: 'info',
      event: 'agents.detect.succeeded',
      at: '2026-07-10T08:30:00.000Z',
      detectedCount: 2,
      detectedAgents: ['claude-code', 'codex'],
    })
    expect(context).toEqual({ detectedCount: 2, detectedAgents: ['claude-code', 'codex'] })
  })

  it('marks deterministic browser preview diagnostics explicitly', () => {
    window.history.replaceState({}, '', '/?onboardingPreview=1#/onboarding')
    const listener = vi.fn()
    window.addEventListener('superdev:onboarding', listener, { once: true })

    emitOnboardingDiagnostic('agents.detect.succeeded', 'info')

    expect((listener.mock.calls[0][0] as CustomEvent).detail).toMatchObject({
      mode: 'preview',
      event: 'agents.detect.succeeded',
    })
  })
})
