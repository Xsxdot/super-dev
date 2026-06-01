/**
 * Vitest 全局测试隔离配置。
 *
 * 职责：
 *   - 在每个测试用例前恢复桌面端默认中文 locale
 *   - 避免英文 i18n 用例污染共享的 i18n 单例
 *
 * 边界：
 *   - 不清理 Pinia、Router、组件挂载或 API mock
 *   - 不覆盖单个测试显式调用 installTestI18n 切换的语言
 */
import { beforeEach } from 'vitest'
import { setLocale } from '@/i18n'

beforeEach(() => {
  setLocale('zh-CN')
})
