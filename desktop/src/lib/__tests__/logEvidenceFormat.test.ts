/**
 * logEvidenceFormat 测试
 *
 * 职责：
 *   - 验证证据钉子的 cursor、Markdown 和同轨道区间格式
 *   - 防止跨分栏日志被错误串成一个导出区间
 *
 * 边界：
 *   - 不测试 Pinia 生命周期
 *   - 不测试 UI 复制、导出文件写入
 */
import { describe, expect, it } from 'vitest'
import {
  buildEvidenceExportModel,
  compareLogCursors,
  formatEvidenceMarkdown,
  formatLogWithCursor,
  formatPinnedLinesMarkdown,
  nearestLogIndexByCursorTime,
} from '../logEvidenceFormat'
import type { EvidencePin } from '@/stores/logEvidence'
import type { LogEntry } from '@/api/agent'

function makeLog(id: string, deploymentId: string, timestamp: string, message: string): LogEntry {
  return {
    id,
    deployment_id: deploymentId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

function makePin(sequence: number, trackId: string, trackLabel: string, log: LogEntry, note = ''): EvidencePin {
  return {
    id: `pin-${sequence}`,
    panelId: trackId,
    trackId,
    trackLabel,
    sourceKey: log.deployment_id,
    sequence,
    label: `P${sequence}`,
    color: '#58a6ff',
    logId: log.id,
    log: { ...log },
    note,
    createdAt: '2026-06-20T10:00:00.000Z',
  }
}

describe('logEvidenceFormat', () => {
  it('compares cursors by timestamp then id', () => {
    const a = makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z', 'a')
    const b = makeLog('2', 'dep-api', '2026-06-20T10:00:00.000Z', 'b')
    const c = makeLog('3', 'dep-api', '2026-06-20T10:00:01.000Z', 'c')

    expect(compareLogCursors(a, b)).toBeLessThan(0)
    expect(compareLogCursors(c, b)).toBeGreaterThan(0)
  })

  it('formats one log with cursor metadata for row context copy', () => {
    const text = formatLogWithCursor(makeLog('1849', 'dep-api', '2026-06-20T10:32:14.000Z', 'retrying request'))

    expect(text).toContain('deployment_id: dep-api')
    expect(text).toContain('cursor_time: 2026-06-20T10:32:14.000Z')
    expect(text).toContain('cursor_id: 1849')
    expect(text).toContain('message: retrying request')
  })

  it('formats pinned-only Markdown with notes and cursor identity', () => {
    const pin = makePin(
      1,
      'panel-api',
      'api · dev',
      makeLog('1849', 'dep-api', '2026-06-20T10:32:14.000Z', 'retrying request'),
      '第一次出现 retry',
    )

    const markdown = formatPinnedLinesMarkdown([pin])

    expect(markdown).toContain('# SuperDev Log Evidence')
    expect(markdown).toContain('### P1')
    expect(markdown).toContain('- track: api · dev')
    expect(markdown).toContain('- cursor_id: 1849')
    expect(markdown).toContain('- note: 第一次出现 retry')
  })

  it('builds segments only between adjacent pins on the same track', () => {
    const p1 = makePin(1, 'panel-api', 'api · dev', makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z', 'api first'))
    const p2 = makePin(2, 'panel-worker', 'worker · dev', makeLog('2', 'dep-worker', '2026-06-20T10:00:01.000Z', 'worker only'))
    const p3 = makePin(3, 'panel-api', 'api · dev', makeLog('3', 'dep-api', '2026-06-20T10:00:02.000Z', 'api second'))

    const model = buildEvidenceExportModel({
      pins: [p1, p2, p3],
      logsByTrack: {
        'panel-api': [p1.log, p3.log],
        'panel-worker': [p2.log],
      },
      skippedSegmentKeys: new Set(),
    })

    expect(model.tracks).toHaveLength(2)
    expect(model.tracks.find(track => track.trackId === 'panel-api')?.segments.map(segment => segment.key)).toEqual([
      'panel-api:1:3',
    ])
    expect(model.tracks.find(track => track.trackId === 'panel-worker')?.segments).toEqual([])
    expect(model.timeline.map(item => item.label)).toEqual(['P1', 'P2', 'P3'])
  })

  it('renders skipped intervals as omitted gaps with boundary cursors', () => {
    const p1 = makePin(1, 'panel-api', 'api · dev', makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z', 'api first'))
    const p2 = makePin(2, 'panel-api', 'api · dev', makeLog('2', 'dep-api', '2026-06-20T10:00:02.000Z', 'api second'))
    const model = buildEvidenceExportModel({
      pins: [p1, p2],
      logsByTrack: { 'panel-api': [p1.log, p2.log] },
      skippedSegmentKeys: new Set(['panel-api:1:2']),
    })

    const markdown = formatEvidenceMarkdown(model)

    expect(markdown).toContain('### Omitted P1 -> P2')
    expect(markdown).toContain('- reason: user skipped this interval')
    expect(markdown).toContain('- from_cursor_id: 1')
    expect(markdown).toContain('- to_cursor_id: 2')
  })

  it('finds the nearest loaded log by timestamp and cursor id', () => {
    const logs = [
      makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z', 'first'),
      makeLog('2', 'dep-api', '2026-06-20T10:00:04.000Z', 'second'),
      makeLog('3', 'dep-api', '2026-06-20T10:00:09.000Z', 'third'),
    ]

    expect(nearestLogIndexByCursorTime(logs, '2026-06-20T10:00:05.000Z', '2')).toBe(1)
  })
})
