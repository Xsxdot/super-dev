/**
 * timeDisplay 测试 UI 相对时间格式化。
 *
 * 职责：
 *   - 验证 agent 最近检测时间展示所需的秒/分钟/小时粒度
 *
 * 边界：
 *   - 不读取系统 locale
 *   - 不格式化绝对日期
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatRelativeAge } from '@/lib/timeDisplay'

describe('formatRelativeAge', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('formats seconds, minutes, and hours', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-03T10:00:00Z'))

    expect(formatRelativeAge('2026-06-03T09:59:48Z', count => `${count}s ago`, count => `${count}m ago`, count => `${count}h ago`)).toBe('12s ago')
    expect(formatRelativeAge('2026-06-03T09:55:00Z', count => `${count}s ago`, count => `${count}m ago`, count => `${count}h ago`)).toBe('5m ago')
    expect(formatRelativeAge('2026-06-03T07:00:00Z', count => `${count}s ago`, count => `${count}m ago`, count => `${count}h ago`)).toBe('3h ago')
  })

  it('returns empty string for empty or invalid timestamp', () => {
    expect(formatRelativeAge('', String, String, String)).toBe('')
    expect(formatRelativeAge('not-a-date', String, String, String)).toBe('')
  })
})
