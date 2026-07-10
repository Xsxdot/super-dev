/**
 * onboardingPreview 测试开发预览激活边界与七 Connector 夹具。
 *
 * 职责：
 *   - 验证显式 query 只在无 Tauri runtime 的开发浏览器中启用夹具
 *   - 验证七个生产 ID、support level 与 Kimi Code Partial 结果
 *
 * 边界：
 *   - 不调用真实 Tauri command，不修改 Agent 配置
 */
import { afterEach, describe, expect, it } from 'vitest'
import {
  isOnboardingPreviewMode,
  previewConnectorOutcome,
  previewConnectorSummaries,
} from '../onboardingPreview'

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

describe('previewConnectorSummaries', () => {
  it('exposes seven production connectors with derived support levels and mixed detection', () => {
    const summaries = previewConnectorSummaries()
    expect(summaries.map(item => [item.descriptor.id, item.descriptor.support_level, item.state.detected])).toEqual([
      ['claude-code', 'full', true],
      ['codex', 'full', true],
      ['cursor', 'full', false],
      ['opencode', 'standard', true],
      ['openclaw', 'standard', false],
      ['hermes', 'full', true],
      ['kimi-code', 'standard', false],
    ])
    // 无 TypeScript 白名单：所有展示字段来自 descriptor/state。
    for (const item of summaries) {
      expect(item.descriptor.display_name.length).toBeGreaterThan(0)
      expect(item.descriptor.integrations).toHaveLength(3)
    }
  })
})

describe('previewConnectorOutcome', () => {
  it('keeps working MCP for kimi-code while overall result stays partial', () => {
    const outcome = previewConnectorOutcome('kimi-code')
    expect(outcome.result).toBe('partial')
    expect(outcome.integrations.find(item => item.capability === 'mcp')?.result).toBe('installed')
    expect(outcome.integrations.find(item => item.capability === 'session_hook')?.result).toBe('needs_action')
    expect(outcome.message || outcome.manual_instructions?.summary).toBeTruthy()
  })
})
