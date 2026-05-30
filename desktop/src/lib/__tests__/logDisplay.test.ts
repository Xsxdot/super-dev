// 日志显示列表测试：覆盖书签标记、折叠行冻结快照和标记外续增显示。
//
// 职责：
//   - 验证 done 状态下标记区间的列表切分
//   - 验证折叠行继续增长时不会挤出已冻结的书签内容
//
// 边界：
//   - 不挂载 Vue 组件，不测试 DOM 样式和滚动行为
import { describe, it, expect } from 'vitest'
import { makeDisplayItems, computeDisplayStats } from '../logDisplay'
import type { DisplayLogEntry } from '../logEngine'

function makeLog(id: number, ts: string, repeatCount = 1): DisplayLogEntry {
  return {
    id,
    deployment_id: 'svc',
    run_id: 'run',
    timestamp: ts,
    level: 'INFO',
    message: `msg-${id}`,
    stream: 'stdout',
    normalized_message: `msg-${id}`,
    repeat_count: repeatCount,
  }
}

describe('makeDisplayItems', () => {
  const markers = { start: 'start-id', end: 'end-id' }

  it('done 状态用 live logs 切分，标记区间内外均可见', () => {
    const start = new Date('2026-05-20T10:38:53.000Z')
    const end = new Date('2026-05-20T10:39:32.000Z')
    const logs = [
      makeLog(1, '2026-05-20T10:38:52.000Z'),
      makeLog(2, '2026-05-20T10:38:54.000Z'),
      makeLog(3, '2026-05-20T10:39:00.000Z'),
      makeLog(4, '2026-05-20T10:39:40.000Z'),
    ]

    const items = makeDisplayItems(
      logs,
      { state: 'done', startTime: start, endTime: end },
      markers,
    )

    const startIdx = items.findIndex(i => i.kind === 'markerStart')
    const endIdx = items.findIndex(i => i.kind === 'markerEnd')
    const between = items.slice(startIdx + 1, endIdx).filter(i => i.kind === 'entry')
    expect(between.map(i => (i as { log: DisplayLogEntry }).log.id)).toEqual([2, 3])
    const afterEnd = items.slice(endIdx + 1).filter(i => i.kind === 'entry')
    expect(afterEnd.map(i => (i as { log: DisplayLogEntry }).log.id)).toEqual([4])
  })

  it('done 后折叠行 id 与书签相同仍出现在 after 段', () => {
    const start = new Date('2026-05-20T10:38:53.000Z')
    const end = new Date('2026-05-20T10:39:32.000Z')
    const logs = [
      makeLog(99, '2026-05-20T10:38:54.000Z'),
      makeLog(99, '2026-05-20T10:40:00.000Z'),
    ]

    const items = makeDisplayItems(
      logs,
      { state: 'done', startTime: start, endTime: end },
      markers,
    )

    const endIdx = items.findIndex(i => i.kind === 'markerEnd')
    const afterEnd = items.slice(endIdx + 1).filter(i => i.kind === 'entry')
    expect(afterEnd.length).toBeGreaterThan(0)
    expect(afterEnd[afterEnd.length - 1].kind === 'entry' && (afterEnd[afterEnd.length - 1] as { log: DisplayLogEntry }).log.id).toBe(99)
  })

  it('done 状态只插入标记，不用 lockedLogs 替换 live 日志流', () => {
    const start = new Date('2026-05-20T12:25:46.000Z')
    const end = new Date('2026-05-20T12:26:07.000Z')
    const logs = [
      makeLog(1, '2026-05-20T12:25:45.000Z', 23),
      makeLog(2, '2026-05-20T12:25:50.000Z', 2),
      makeLog(3, '2026-05-20T12:25:55.000Z'),
      makeLog(4, '2026-05-20T12:26:06.000Z', 23),
      makeLog(5, '2026-05-20T12:26:12.000Z', 2),
    ]

    const items = makeDisplayItems(
      logs,
      {
        state: 'done',
        startTime: start,
        endTime: end,
        lockedLogs: [makeLog(99, '2026-05-20T12:25:55.000Z')],
      },
      markers,
    )

    expect(items.map(item => (item.kind === 'entry' ? item.log.id : item.kind))).toEqual([
      1,
      'markerStart',
      2,
      3,
      4,
      'markerEnd',
      5,
    ])
  })

  it('在历史边界后插入历史消息分隔线', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:02.000Z'),
      makeLog(3, '2026-05-21T10:00:03.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, {
      timestamp: '2026-05-21T10:00:02.000Z',
      id: 2,
    })

    expect(items.map(item => item.kind)).toEqual(['entry', 'entry', 'historySeparator', 'entry'])
  })

  it('历史分隔线不参与统计', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:02.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, {
      timestamp: '2026-05-21T10:00:01.000Z',
      id: 1,
    })

    expect(computeDisplayStats(items).total).toBe(2)
  })
})
