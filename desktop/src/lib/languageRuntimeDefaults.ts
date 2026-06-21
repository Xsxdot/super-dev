/**
 * Language runtime 默认配置与能力判断。
 *
 * 职责：
 *   - 为新建本地 managed deployment 生成语言优先的 runtime 默认值
 *   - 判断 language runtime 是否具备 debug_launch 所需的显式入口
 *
 * 边界：
 *   - 不调用后端 provider schema；这里只提供 UI 层的保守默认值
 *   - 不处理 legacy command runtime 的兼容转换
 */
import type { RuntimeConfig, ServiceLanguage } from '@/api/agent'

function nonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim() !== ''
}

export function defaultLanguageRuntime(language?: ServiceLanguage, defaultWorkDir = ''): RuntimeConfig | undefined {
  if (!language) return undefined
  const base = { type: 'language' as const, cwd: defaultWorkDir, env: {} }
  if (language === 'go') return { ...base, config: { program: '.' } }
  if (language === 'node') return { ...base, config: { package_manager: 'pnpm', script: 'dev' } }
  if (language === 'python') return { ...base, config: { program: 'main.py' } }
  if (language === 'java' || language === 'kotlin') return { ...base, config: { classpath: 'build/classes' } }
  if (language === 'rust') return { ...base, config: { program: 'target/debug/app', build: 'cargo' } }
  if (language === 'cpp') return { ...base, config: { program: 'build/app' } }
  return undefined
}

export function defaultManagedRuntime(language?: ServiceLanguage, defaultWorkDir = ''): RuntimeConfig {
  return defaultLanguageRuntime(language, defaultWorkDir) ?? {
    type: 'language',
    cwd: defaultWorkDir,
    env: {},
    config: {},
  }
}

export function languageRuntimeSupportsDebugLaunch(language: ServiceLanguage | undefined, runtime?: RuntimeConfig): boolean {
  if (!language || runtime?.type !== 'language') return false
  const config = runtime.config ?? {}
  // 逃生口只保证启动，不保证 provider 能构造 DAP launch 配置。
  if (nonEmptyString(config.runtime_executable)) return false
  if (language === 'go') return true
  if (language === 'node' || language === 'python' || language === 'java' || language === 'kotlin' || language === 'rust' || language === 'cpp') {
    return nonEmptyString(config.program)
  }
  return false
}
