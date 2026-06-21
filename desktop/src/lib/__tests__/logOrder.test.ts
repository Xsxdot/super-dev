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
import { appendLive, insertSorted, LIVE_REORDER_WINDOW } from '../logOrder'
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

  describe('appendLive 有限乱序窗口', () => {
    it('顺序到达直接追加到尾', () => {
      const logs = [entry('a', '2024-01-01T00:00:01.000Z'), entry('b', '2024-01-01T00:00:02.000Z')]
      appendLive(logs, entry('c', '2024-01-01T00:00:03.000Z'))
      expect(logs.map(log => log.id)).toEqual(['a', 'b', 'c'])
    })

    it('窗口内轻微乱序就地纠正', () => {
      const logs = [entry('a', '2024-01-01T00:00:01.000Z'), entry('c', '2024-01-01T00:00:03.000Z')]
      appendLive(logs, entry('b', '2024-01-01T00:00:02.000Z'))
      expect(logs.map(log => log.id)).toEqual(['a', 'b', 'c'])
    })

    it('窗口外的迟到日志放尾部，不回溯到可视区上方', () => {
      const logs: DisplayLogEntry[] = []
      for (let i = 0; i < LIVE_REORDER_WINDOW + 5; i++) {
        logs.push(entry(`x${i}`, new Date(10_000 + i).toISOString()))
      }
      appendLive(logs, entry('late', new Date(1).toISOString()))
      expect(logs[logs.length - 1].id).toBe('late')
    })

    it('重复 id 原地覆盖', () => {
      const logs = [entry('a', '2024-01-01T00:00:01.000Z'), entry('b', '2024-01-01T00:00:02.000Z')]
      appendLive(logs, entry('b', '2024-01-01T00:00:02.000Z', 'updated'))
      expect(logs).toHaveLength(2)
      expect(logs.find(log => log.id === 'b')?.message).toBe('updated')
    })
  })
})
