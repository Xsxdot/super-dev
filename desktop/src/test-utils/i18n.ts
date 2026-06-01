/**
 * 测试用 i18n 安装工具。
 *
 * 职责：
 *   - 为 Vue Test Utils mount 提供已设置语言的 i18n plugin
 *
 * 边界：
 *   - 不封装 Pinia、Router 或组件 stub
 */
import { i18n, setLocale, type SupportedLocale } from '@/i18n'

// installTestI18n 将单例 i18n 调整到指定语言并返回可传给 mount.global.plugins 的插件。
export function installTestI18n(locale: SupportedLocale = 'zh-CN') {
  setLocale(locale)
  return i18n
}
