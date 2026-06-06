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
import { formatDuration, formatRelativeAge } from '@/lib/timeDisplay'

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

describe('formatDuration', () => {
  it('formats milliseconds as compact elapsed time', () => {
    expect(formatDuration(0)).toBe('0s')
    expect(formatDuration(999)).toBe('0s')
    expect(formatDuration(12_000)).toBe('12s')
    expect(formatDuration(125_000)).toBe('2m 5s')
    expect(formatDuration(3_665_000)).toBe('1h 1m')
  })

  it('returns empty string for invalid duration input', () => {
    expect(formatDuration(undefined)).toBe('')
    expect(formatDuration(null)).toBe('')
    expect(formatDuration(Number.NaN)).toBe('')
    expect(formatDuration(-1)).toBe('')
  })
})
