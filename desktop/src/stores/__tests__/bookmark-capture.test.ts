// 书签捕获测试：验证结束标记时冻结日志快照的边界规则。
//
// 职责：
//   - 覆盖按时间区间捕获普通日志
//   - 覆盖折叠行 timestamp 后移时通过 capturedIds 保留
//
// 边界：
//   - 不测试 Pinia store 生命周期，store 行为由 bookmark.test.ts 覆盖
import { describe, it, expect } from 'vitest'
import { captureLockedLogs } from '../bookmark'
import type { LogEntry } from '@/api/agent'

function makeLog(id: number, ts: string, repeat = 1): LogEntry {
  return {
    id,
    deployment_id: 'svc',
    run_id: 'run',
    timestamp: ts,
    level: 'INFO',
    message: `msg-${id}`,
    stream: 'stdout',
    repeat_count: repeat,
  }
}

describe('captureLockedLogs', () => {
  it('只保留区间内日志并按时间排序', () => {
    const start = new Date('2026-05-20T10:38:53.000Z')
    const end = new Date('2026-05-20T10:39:32.000Z')
    const logs = [
      makeLog(1, '2026-05-20T10:38:52.000Z'),
      makeLog(2, '2026-05-20T10:38:54.000Z'),
      makeLog(3, '2026-05-20T10:39:00.000Z'),
      makeLog(4, '2026-05-20T10:39:40.000Z'),
    ]
    const locked = captureLockedLogs(logs, start, end)
    expect(locked.map(l => l.id)).toEqual([2, 3])
  })

  it('capturedIds 保留结束后 timestamp 越过 end 的折叠行', () => {
    const start = new Date('2026-05-20T10:38:53.000Z')
    const end = new Date('2026-05-20T10:39:32.000Z')
    const logs = [
      makeLog(10, '2026-05-20T10:38:54.000Z'),
      makeLog(11, '2026-05-20T10:40:00.000Z', 50),
    ]
    const locked = captureLockedLogs(logs, start, end, new Set([10, 11]))
    expect(locked.map(l => l.id)).toEqual([10, 11])
    expect(locked[1].repeat_count).toBe(50)
  })
})
