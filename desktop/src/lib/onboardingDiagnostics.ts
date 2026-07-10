/**
 * onboardingDiagnostics 统一派发首次启动流程的结构化诊断事件。
 *
 * 职责：
 *   - 为检测、安装、手动接入和完成动作补齐可检索的事件上下文
 *   - 统一 scope、level、event 与时间戳字段
 *
 * 边界：
 *   - 不上传事件，上传由 frontendDiagnostics bridge 负责
 *   - 不接收配置正文、凭据、绝对路径或剪贴板内容
 */
import { isOnboardingPreviewMode } from '@/dev/onboardingPreview'

export type OnboardingDiagnosticLevel = 'debug' | 'info' | 'warn' | 'error'

/**
 * emitOnboardingDiagnostic 派发一条首次启动结构化诊断事件。
 *
 * 参数：
 *   - event: 稳定的点分事件名
 *   - level: 诊断级别
 *   - context: 不含敏感内容的业务上下文
 *
 * 注意：
 *   - 保留字段由本函数覆盖，调用方不能伪造 scope、level、event 或 at
 */
export function emitOnboardingDiagnostic(
  event: string,
  level: OnboardingDiagnosticLevel,
  context: Record<string, unknown> = {},
) {
  window.dispatchEvent(new CustomEvent('superdev:onboarding', {
    detail: {
      ...context,
      scope: 'onboarding',
      mode: isOnboardingPreviewMode() ? 'preview' : 'runtime',
      level,
      event,
      at: new Date().toISOString(),
    },
  }))
}
