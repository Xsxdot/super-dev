/**
 * i18n 模块测试桌面端语言解析与持久化。
 *
 * 职责：
 *   - 验证 localStorage、浏览器语言和默认语言的解析优先级
 *   - 验证 setLocale 会更新 i18n 实例、localStorage 和 document.lang
 *
 * 边界：
 *   - 不渲染任何 Vue 组件
 *   - 不测试第三方 vue-i18n 内部行为
 */
import { beforeEach, describe, expect, it } from 'vitest'
import zhCN from '@/i18n/locales/zh-CN'
import enUS from '@/i18n/locales/en-US'
import {
  LOCALE_STORAGE_KEY,
  i18n,
  isSupportedLocale,
  resolveInitialLocale,
  setLocale,
} from '@/i18n'

function flattenKeys(value: unknown, prefix = ''): string[] {
  if (!value || typeof value !== 'object') return [prefix]
  return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
    flattenKeys(child, prefix ? `${prefix}.${key}` : key),
  )
}

describe('i18n', () => {
  beforeEach(() => {
    localStorage.clear()
    setLocale('zh-CN')
  })

  it('识别支持的 locale', () => {
    expect(isSupportedLocale('zh-CN')).toBe(true)
    expect(isSupportedLocale('en-US')).toBe(true)
    expect(isSupportedLocale('fr-FR')).toBe(false)
    expect(isSupportedLocale(null)).toBe(false)
  })

  it('localStorage 中的支持语言优先', () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'en-US')

    expect(resolveInitialLocale(['zh-CN'])).toBe('en-US')
  })

  it('忽略 localStorage 中不支持的语言并从浏览器语言推断英文', () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'fr-FR')

    expect(resolveInitialLocale(['en-GB', 'zh-CN'])).toBe('en-US')
  })

  it('无已保存语言且浏览器不是英文时默认中文', () => {
    expect(resolveInitialLocale(['ja-JP'])).toBe('zh-CN')
    expect(resolveInitialLocale([])).toBe('zh-CN')
  })

  it('setLocale 持久化语言并更新 document.lang', () => {
    setLocale('en-US')

    expect(i18n.global.locale.value).toBe('en-US')
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('en-US')
    expect(document.documentElement.lang).toBe('en-US')
  })

  it('zh-CN and en-US message keys stay aligned', () => {
    expect(flattenKeys(enUS).sort()).toEqual(flattenKeys(zhCN).sort())
  })

  it('contains run console labels in both locales', () => {
    expect(zhCN.runConsole.waitingOutput).toBeTruthy()
    expect(zhCN.runConsole.backToBottom).toBeTruthy()
    expect(enUS.runConsole.waitingOutput).toBeTruthy()
    expect(enUS.runConsole.backToBottom).toBeTruthy()
    expect(zhCN.overview.pipeline.running).toBeTruthy()
    expect(enUS.overview.pipeline.running).toBeTruthy()
  })
})
