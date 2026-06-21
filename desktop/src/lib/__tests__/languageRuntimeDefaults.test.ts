/**
 * languageRuntimeDefaults 测试锁定桌面端语言 runtime 默认值和调试入口判断。
 *
 * 职责：
 *   - 确认桌面端能为 Agent 已注册语言生成保守的 language runtime 初始配置
 *   - 确认 debug launch 只在具备明确入口时开放
 *
 * 边界：
 *   - 不测试后端 provider schema；schema 字段由 Agent API 单独负责
 *   - 不测试 Vue 表单渲染；这里只覆盖纯函数行为
 */
import { describe, expect, it } from 'vitest'
import type { RuntimeConfig, ServiceLanguage } from '@/api/agent'
import { defaultLanguageRuntime, languageRuntimeSupportsDebugLaunch } from '@/lib/languageRuntimeDefaults'

function languageRuntime(config: Record<string, unknown>): RuntimeConfig {
  return {
    type: 'language',
    cwd: '/repo/api',
    env: {},
    config,
  }
}

describe('languageRuntimeDefaults', () => {
  it('creates conservative language runtime defaults for JVM and native providers', () => {
    expect(defaultLanguageRuntime('java', '/repo/api')).toEqual(languageRuntime({ classpath: 'build/classes' }))
    expect(defaultLanguageRuntime('kotlin', '/repo/api')).toEqual(languageRuntime({ classpath: 'build/classes' }))
    expect(defaultLanguageRuntime('rust', '/repo/api')).toEqual(languageRuntime({ program: 'target/debug/app', build: 'cargo' }))
    expect(defaultLanguageRuntime('cpp', '/repo/api')).toEqual(languageRuntime({ program: 'build/app' }))
  })

  it('requires an explicit program before debug launch for JVM and native providers', () => {
    for (const language of ['java', 'kotlin', 'rust', 'cpp'] satisfies ServiceLanguage[]) {
      expect(languageRuntimeSupportsDebugLaunch(language, languageRuntime({}))).toBe(false)
      expect(languageRuntimeSupportsDebugLaunch(language, languageRuntime({ program: 'app.Main' }))).toBe(true)
    }
  })

  it('does not advertise debug launch when the custom runtime escape hatch is active', () => {
    for (const language of ['go', 'node', 'python', 'java', 'kotlin', 'rust', 'cpp'] satisfies ServiceLanguage[]) {
      expect(languageRuntimeSupportsDebugLaunch(language, languageRuntime({
        program: 'app.Main',
        runtime_executable: 'make',
      }))).toBe(false)
    }
  })
})
