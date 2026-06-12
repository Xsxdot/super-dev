/**
 * runtimeMetrics tests.
 *
 * Responsibilities:
 *   - Verify user-facing runtime metric formatting
 *   - Verify health classification and CPU bar sizing
 *
 * Boundaries:
 *   - Does not render Vue components
 */
import { describe, expect, it } from 'vitest'
import {
  cpuBarWidth,
  formatBytes,
  formatPercent,
  formatRestarts,
  formatUptime,
  isAbnormalHealth,
} from '../runtimeMetrics'

describe('runtimeMetrics', () => {
  it('formats known metrics', () => {
    expect(formatPercent(12.345)).toBe('12.3%')
    expect(formatBytes(128 * 1024 * 1024)).toBe('128 MiB')
    expect(formatBytes(2.5 * 1024 * 1024 * 1024)).toBe('2.5 GiB')
    expect(formatUptime(3661)).toBe('1h 1m')
    expect(formatUptime(59)).toBe('0m')
    expect(formatRestarts(2)).toBe('2')
  })

  it('formats missing metrics with caller-selected empty text', () => {
    expect(formatPercent(null, '—')).toBe('—')
    expect(formatBytes(null, '—')).toBe('—')
    expect(formatUptime(null, '—')).toBe('—')
    expect(formatRestarts(null, '—')).toBe('—')
  })

  it('classifies abnormal health and clamps CPU bar width', () => {
    expect(isAbnormalHealth('failed')).toBe(true)
    expect(isAbnormalHealth('unknown')).toBe(true)
    expect(isAbnormalHealth('running')).toBe(false)
    expect(cpuBarWidth(null)).toBeNull()
    expect(cpuBarWidth(-3)).toBe(0)
    expect(cpuBarWidth(28.4)).toBe(28.4)
    expect(cpuBarWidth(118)).toBe(100)
  })

  it('treats debug-running as a healthy runtime state', () => {
    expect(isAbnormalHealth('debug-running')).toBe(false)
  })
})
