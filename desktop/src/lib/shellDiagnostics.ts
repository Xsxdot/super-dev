/**
 * shellDiagnostics 统一派发桌面窗口壳层的结构化诊断事件。
 *
 * 职责：
 *   - 记录自绘标题栏的窗口动作及其结果
 *   - 为 capability 或 Tauri 调用失败提供可检索信号
 *
 * 边界：
 *   - 不上传事件，上传由 frontendDiagnostics bridge 负责
 *   - 不接收错误正文、绝对路径或用户内容
 */
export type ShellDiagnosticLevel = 'debug' | 'info' | 'warn' | 'error'

/**
 * emitShellDiagnostic 派发一条桌面窗口壳层诊断事件。
 *
 * 参数：
 *   - event: 稳定的点分事件名
 *   - level: 诊断级别
 *   - context: 不含敏感信息的动作上下文
 */
export function emitShellDiagnostic(
  event: string,
  level: ShellDiagnosticLevel,
  context: Record<string, unknown> = {},
) {
  window.dispatchEvent(new CustomEvent('superdev:shell', {
    detail: {
      ...context,
      scope: 'shell',
      level,
      event,
      at: new Date().toISOString(),
    },
  }))
}
