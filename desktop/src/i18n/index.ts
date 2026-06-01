/**
 * 桌面端 i18n 入口。
 *
 * 职责：
 *   - 定义支持的语言和显示名称
 *   - 创建 vue-i18n 实例
 *   - 解析、持久化并切换当前语言
 *
 * 边界：
 *   - 不读取 agent 设置
 *   - 不翻译用户数据、日志内容、命令输出或后端自由文本错误
 */
import { createI18n } from 'vue-i18n'
import enUS from './locales/en-US'
import zhCN from './locales/zh-CN'

export const LOCALE_STORAGE_KEY = 'superdev.locale.v1'

export const SUPPORTED_LOCALES = ['zh-CN', 'en-US'] as const
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number]

export const SUPPORTED_LOCALE_OPTIONS: Array<{ value: SupportedLocale; label: string }> = [
  { value: 'zh-CN', label: 'Chinese (Simplified)' },
  { value: 'en-US', label: 'English' },
]

// isSupportedLocale 判断传入值是否属于桌面端当前支持的语言集合。
export function isSupportedLocale(value: unknown): value is SupportedLocale {
  return typeof value === 'string' && SUPPORTED_LOCALES.includes(value as SupportedLocale)
}

function storedLocale(): SupportedLocale | null {
  try {
    const stored = localStorage.getItem(LOCALE_STORAGE_KEY)
    return isSupportedLocale(stored) ? stored : null
  } catch {
    return null
  }
}

function browserLanguages(): string[] {
  if (typeof navigator === 'undefined') return []
  if (Array.isArray(navigator.languages) && navigator.languages.length > 0) return [...navigator.languages]
  return navigator.language ? [navigator.language] : []
}

// resolveInitialLocale 按已保存设置、浏览器语言、默认中文的优先级解析初始语言。
export function resolveInitialLocale(languages = browserLanguages()): SupportedLocale {
  const saved = storedLocale()
  if (saved) return saved
  return languages.some((language) => language.toLowerCase().startsWith('en')) ? 'en-US' : 'zh-CN'
}

export const i18n = createI18n({
  legacy: false,
  locale: resolveInitialLocale(),
  fallbackLocale: 'zh-CN',
  messages: {
    'zh-CN': zhCN,
    'en-US': enUS,
  },
})

function applyDocumentLang(locale: SupportedLocale) {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = locale
  }
}

// currentLocale 返回当前 i18n 实例中的语言，异常值会回退到中文。
export function currentLocale(): SupportedLocale {
  const locale = i18n.global.locale.value
  return isSupportedLocale(locale) ? locale : 'zh-CN'
}

// setLocale 切换语言并同步持久化值与 document.lang。
export function setLocale(locale: SupportedLocale) {
  i18n.global.locale.value = locale
  localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  applyDocumentLang(locale)
}

applyDocumentLang(currentLocale())
