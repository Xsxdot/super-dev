// logEvidenceDiagnostics 封装证据工作流的前端诊断事件。
//
// 职责：
//   - 为打钉、备注、复制、导出、时间同步等关键状态变化提供统一事件
//   - 在浏览器环境派发结构化 CustomEvent，便于测试和未来接入桌面日志
//
// 边界：
//   - 不处理业务状态
//   - 不替代用户可见的错误提示
export type EvidenceDiagnosticLevel = 'debug' | 'info' | 'warn' | 'error'

export interface EvidenceDiagnosticContext {
  panelId?: string
  trackId?: string
  pinId?: string
  pinLabel?: string
  deploymentId?: string
  cursorTime?: string
  cursorId?: string
  error?: string
  [key: string]: unknown
}

/**
 * logEvidenceDiagnostic 记录证据工作流关键诊断事件。
 *
 * 参数：
 *   - level: 事件等级
 *   - event: 稳定事件名
 *   - context: 可搜索的上下文
 *
 * 注意：
 *   - 生产代码通过 CustomEvent 暴露结构化事件；开发环境额外写入浏览器控制台
 *   - 不使用 console.log，避免无等级、无结构的调试输出
 */
export function logEvidenceDiagnostic(
  level: EvidenceDiagnosticLevel,
  event: string,
  context: EvidenceDiagnosticContext = {},
) {
  const detail = {
    scope: 'log-evidence',
    level,
    event,
    at: new Date().toISOString(),
    ...context,
  }
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('superdev:log-evidence', { detail }))
  }
  if (import.meta.env.DEV && import.meta.env.MODE !== 'test') {
    const message = `[SuperDev] log evidence ${event}`
    if (level === 'error') console.error(message, detail)
    else if (level === 'warn') console.warn(message, detail)
    else console.info(message, detail)
  }
}
