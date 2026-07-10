/**
 * onboardingPreview 测试开发预览激活边界。
 *
 * 职责：
 *   - 验证显式 query 只在无 Tauri runtime 的开发浏览器中启用夹具
 *
 * 边界：
 *   - 不调用真实 Tauri command，不修改 Agent 配置
 */
import { afterEach, describe, expect, it } from 'vitest'
import { isOnboardingPreviewMode } from '../onboardingPreview'

describe('isOnboardingPreviewMode', () => {
  afterEach(() => {
    window.history.replaceState({}, '', '/')
    Reflect.deleteProperty(window, '__TAURI_INTERNALS__')
  })

  it('requires the explicit query flag and the absence of a Tauri runtime', () => {
    window.history.replaceState({}, '', '/?onboardingPreview=1#/onboarding')
    expect(isOnboardingPreviewMode()).toBe(true)

    Object.defineProperty(window, '__TAURI_INTERNALS__', {
      configurable: true,
      value: {},
    })
    expect(isOnboardingPreviewMode()).toBe(false)
  })
})
