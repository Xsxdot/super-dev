// 日志显示列表测试：覆盖书签标记、折叠行冻结快照和标记外续增显示。
//
// 职责：
//   - 验证 done 状态下标记区间的列表切分
//   - 验证折叠行继续增长时不会挤出已冻结的书签内容
//
// 边界：
//   - 不挂载 Vue 组件，不测试 DOM 样式和滚动行为
import { describe, it, expect } from 'vitest'
import { makeDisplayItems, computeDisplayStats, type LogDisplayItem } from '../logDisplay'
import type { DisplayLogEntry } from '../logEngine'
import type { MirrorEvent } from '@/stores/portMirror'

function makeLog(id: number, ts: string, repeatCount = 1): DisplayLogEntry {
  const logId = String(id)
  return {
    id: logId,
    deployment_id: 'svc',
    run_id: 'run',
    timestamp: ts,
    level: 'INFO',
    message: `msg-${id}`,
    stream: 'stdout',
    repeat_count: repeatCount,
  }
}

function toMs(ts: string): number {
  return new Date(ts).getTime()
}

function makeMirrorEvent(kind: MirrorEvent['kind'], ts: string): MirrorEvent {
  return { deploymentId: 'dep-1', port: 9100, hostName: 'dev-box', kind, at: toMs(ts) }
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
    expect(between.map(i => (i as { log: DisplayLogEntry }).log.id)).toEqual(['2', '3'])
    const afterEnd = items.slice(endIdx + 1).filter(i => i.kind === 'entry')
    expect(afterEnd.map(i => (i as { log: DisplayLogEntry }).log.id)).toEqual(['4'])
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
    expect(afterEnd[afterEnd.length - 1].kind === 'entry' && (afterEnd[afterEnd.length - 1] as { log: DisplayLogEntry }).log.id).toBe('99')
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
      '1',
      'markerStart',
      '2',
      '3',
      '4',
      'markerEnd',
      '5',
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
      id: '2',
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
      id: '1',
    })

    expect(computeDisplayStats(items).total).toBe(2)
  })

  it('按时间插入生命周期分隔线', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:03.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, null, [
      { id: 'life-1', deploymentId: 'dep-1', kind: 'restart', createdAt: '2026-05-21T10:00:02.000Z' },
    ])

    expect(items.map(item => item.kind)).toEqual(['entry', 'lifecycleSeparator', 'entry'])
  })

  it('生命周期分隔线不参与统计', () => {
    const items = makeDisplayItems([makeLog(1, '2026-05-21T10:00:01.000Z')], null, markers, null, [
      { id: 'life-1', deploymentId: 'dep-1', kind: 'start', createdAt: '2026-05-21T10:00:02.000Z' },
    ])

    expect(computeDisplayStats(items).total).toBe(1)
  })

  it('按时间插入断流缺口分隔线且不参与统计', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:03.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, null, [], [
      { id: 'gap-1', time: '2026-05-21T10:00:02.000Z' },
    ])

    expect(items.map(item => item.kind)).toEqual(['entry', 'gapSeparator', 'entry'])
    expect(computeDisplayStats(items).total).toBe(2)
  })

  it('按时间插入端口镜像事件行', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:03.000Z'),
    ]
    const mirrorEvents: MirrorEvent[] = [
      makeMirrorEvent('established', '2026-05-21T10:00:02.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, null, [], [], mirrorEvents)

    expect(items.map(item => item.kind)).toEqual(['entry', 'mirrorEvent', 'entry'])
  })

  it('端口镜像事件行携带 port/hostName/event 且不参与统计', () => {
    const mirrorEvents: MirrorEvent[] = [
      { deploymentId: 'dep-1', port: 5173, hostName: 'dev-box', kind: 'conflict', at: toMs('2026-05-21T10:00:02.000Z') },
    ]

    const items = makeDisplayItems([], null, markers, null, [], [], mirrorEvents)

    expect(items).toEqual([
      expect.objectContaining({
        kind: 'mirrorEvent',
        port: 5173,
        hostName: 'dev-box',
        event: 'conflict',
      }),
    ])
    expect(computeDisplayStats(items).total).toBe(0)
  })

  it('多条端口镜像事件按时间排序插入，不受入参顺序影响', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:10.000Z'),
    ]
    // 刻意乱序传入（failed 在前，established 在后），验证插入结果仍按 at 排序，
    // 而不是按数组顺序排列。
    const mirrorEvents: MirrorEvent[] = [
      makeMirrorEvent('failed', '2026-05-21T10:00:07.000Z'),
      makeMirrorEvent('established', '2026-05-21T10:00:03.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, null, [], [], mirrorEvents)

    expect(items.map(item => item.kind)).toEqual(['entry', 'mirrorEvent', 'mirrorEvent', 'entry'])
    const mirrorKinds = items
      .filter((item): item is Extract<LogDisplayItem, { kind: 'mirrorEvent' }> => item.kind === 'mirrorEvent')
      .map(item => item.event)
    expect(mirrorKinds).toEqual(['established', 'failed'])
  })

  it('两条端口镜像事件仅 hostName 不同（同 deploymentId/port/kind/at）时 id 也必须不同', () => {
    // 复现场景：同一 deployment 的两个副本跑在不同 host 上，同一端口，WS 一次
    // diff 里在同一毫秒内都跃迁到 established（diffSnapshots 在一个同步循环里
    // 逐条 Date.now()，撞同一毫秒很常见）。id 里如果不带 host 判别符，两条本质
    // 不同（host 不同）的事件会拿到完全相同的 id，破坏虚拟列表 getItemKey 的
    // 唯一性契约——这正是 store 自己的 mirrorEntryKey（portMirror.ts:38-39）
    // 早就用 host_id::deployment_id::port 三元组规避掉的同一个坑。
    const sharedAt = toMs('2026-05-21T10:00:02.000Z')
    const mirrorEvents: MirrorEvent[] = [
      { deploymentId: 'dep-1', port: 9100, hostName: 'dev-box-a', kind: 'established', at: sharedAt },
      { deploymentId: 'dep-1', port: 9100, hostName: 'dev-box-b', kind: 'established', at: sharedAt },
    ]

    const items = makeDisplayItems([], null, markers, null, [], [], mirrorEvents)

    const ids = items
      .filter((item): item is Extract<LogDisplayItem, { kind: 'mirrorEvent' }> => item.kind === 'mirrorEvent')
      .map(item => item.id)
    expect(ids).toHaveLength(2)
    expect(new Set(ids).size).toBe(2)
  })

  it('空 mirrorEvents 数组对显示列表零影响', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:03.000Z'),
    ]

    const withoutArg = makeDisplayItems(logs, null, markers, null, [], [])
    const withEmptyArray = makeDisplayItems(logs, null, markers, null, [], [], [])

    expect(withEmptyArray).toEqual(withoutArg)
    expect(withEmptyArray.map(item => item.kind)).toEqual(['entry', 'entry'])
  })
})
