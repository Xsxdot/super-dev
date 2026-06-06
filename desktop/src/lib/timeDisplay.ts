/**
 * UI 时间展示工具。
 *
 * 职责：
 *   - 将 ISO 时间转换为相对时间文案
 *
 * 边界：
 *   - 不持有时钟状态
 *   - 不决定具体语言文案，由调用方传入 formatter
 */
export function formatRelativeAge(
  iso: string | undefined,
  secondsText: (count: number) => string,
  minutesText: (count: number) => string,
  hoursText: (count: number) => string,
): string {
  if (!iso) return ''
  const time = Date.parse(iso)
  if (Number.isNaN(time)) return ''
  const seconds = Math.max(0, Math.floor((Date.now() - time) / 1000))
  if (seconds < 60) return secondsText(seconds)
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return minutesText(minutes)
  return hoursText(Math.floor(minutes / 60))
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return ''
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}
