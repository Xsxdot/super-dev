/**
 * logOrder 测试日志条目的 string id 排序与去重。
 *
 * 职责：
 *   - 验证同时间戳时按不透明 string id 字典序兜底
 *   - 验证重复 string id 原地覆盖
 *
 * 边界：
 *   - 不测试 WebSocket 或 store，只测试排序纯函数
 */
import { describe, expect, it } from 'vitest'
import { insertSorted } from '../logOrder'
import type { DisplayLogEntry } from '../logEngine'

function entry(id: string, timestamp = '2024-01-01T00:00:00.000Z', message = id): DisplayLogEntry {
  return {
    id,
    deployment_id: 'dep-1',
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
    normalized_message: message,
    repeat_count: 1,
  }
}

describe('logOrder', () => {
  it('orders same-timestamp entries by lexical string id', () => {
    const logs: DisplayLogEntry[] = []

    insertSorted(logs, entry('9'))
    insertSorted(logs, entry('100'))

    expect(logs.map(log => log.id)).toEqual(['100', '9'])
  })

  it('dedupes by exact string id and keeps replacement sorted', () => {
    const logs: DisplayLogEntry[] = []

    insertSorted(logs, entry('9', '2024-01-01T00:00:01.000Z', 'old'))
    insertSorted(logs, entry('9', '2024-01-01T00:00:02.000Z', 'new'))

    expect(logs).toHaveLength(1)
    expect(logs[0].message).toBe('new')
  })
})
