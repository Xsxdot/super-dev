/**
 * 组件级 i18n 轻量访问器。
 *
 * 职责：
 *   - 基于桌面端 i18n 单例提供可响应 locale 变化的 t 函数
 *   - 让深层纯组件和既有单元测试无需依赖 app.use 安装上下文
 *
 * 边界：
 *   - 不创建新的 i18n 实例
 *   - 不解析或持久化语言设置
 */
import { computed } from 'vue'
import { i18n } from '@/i18n'

// useAppI18n 返回绑定全局 i18n 单例的翻译函数。
export function useAppI18n() {
  const locale = computed(() => i18n.global.locale.value)

  function t(key: string, params?: Record<string, unknown>) {
    // 显式读取 locale，确保模板重新渲染时会跟随语言变化。
    locale.value
    return i18n.global.t(key, params ?? {})
  }

  return { t }
}
