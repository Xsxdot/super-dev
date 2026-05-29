import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, afterEach, describe, it, expect, vi } from 'vitest'
import { useBookmarkStore } from '../bookmark'
import type { LogEntry } from '@/api/agent'

let nextLogId = 1
function makeLog(message: string, ts: string): LogEntry {
  return {
    id: nextLogId++,
    service_id: 'svc',
    run_id: 'run',
    timestamp: ts,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('bookmarkStore', () => {
  beforeEach(() => {
    nextLogId = 1
    vi.useFakeTimers()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('startBookmark 后状态为 recording', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    expect(store.getBookmark('p1')?.state).toBe('recording')
  })

  it('endBookmark 后状态为 done', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    store.endBookmark('p1')
    expect(store.getBookmark('p1')?.state).toBe('done')
  })

  it('endBookmark 用 capturedIds 冻结区间内日志（含折叠行）', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const start = store.getBookmark('p1')!.startTime!
    vi.advanceTimersByTime(5000)
    const t1 = new Date(start.getTime() + 1000).toISOString()
    const t2 = new Date(start.getTime() + 2000).toISOString()
    const afterEndTs = new Date(start.getTime() + 60_000).toISOString()
    store.endBookmark(
      'p1',
      [makeLog('a', t1), makeLog('b', t2), makeLog('folded', afterEndTs)],
      new Set([1, 2, 3]),
    )
    const done = store.getBookmark('p1')!
    expect(done.lockedLogs).toHaveLength(3)
    expect(done.lockedLogs.map(l => l.message)).toEqual(['a', 'b', 'folded'])
  })

  it('endBookmark 保留录制期间已捕获但结束快照缺失的日志', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const start = store.getBookmark('p1')!.startTime!
    vi.advanceTimersByTime(5000)
    const capturedTs = new Date(start.getTime() + 1000).toISOString()
    const finalTs = new Date(start.getTime() + 2000).toISOString()
    const captured = makeLog('visible-before-final-capture', capturedTs)
    const folded = makeLog('folded-heartbeat', finalTs)

    store.appendToBookmark('p1', captured)
    store.endBookmark(
      'p1',
      [{ ...folded, repeat_count: 7 }],
      new Set([captured.id, folded.id]),
    )

    expect(store.getBookmark('p1')!.lockedLogs.map(l => l.message)).toEqual([
      'visible-before-final-capture',
      'folded-heartbeat',
    ])
  })

  it('formatBookmark 按 repeat_count 展开多行', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const start = store.getBookmark('p1')!.startTime!
    vi.advanceTimersByTime(1000)
    const ts = new Date(start.getTime() + 100).toISOString()
    store.endBookmark('p1', [{ ...makeLog('x', ts), repeat_count: 3 }], new Set([1]))
    const text = store.formatBookmark('p1')
    expect(text.split('\n')).toHaveLength(3)
  })

  it('appendToBookmark 追加录制期间的日志', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const startTs = store.getBookmark('p1')!.startTime!
    const afterTs = new Date(startTs.getTime() + 1000).toISOString()
    store.appendToBookmark('p1', makeLog('hello', afterTs))
    expect(store.getBookmark('p1')?.lockedLogs).toHaveLength(1)
  })

  it('endBookmark 从 captureLogs 填充 lockedLogs', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const midTs = new Date().toISOString()
    const logs = [makeLog('in range', midTs)]
    store.endBookmark('p1', logs)
    expect(store.getBookmark('p1')?.lockedLogs).toHaveLength(1)
    expect(store.getBookmark('p1')?.lockedLogs[0].message).toBe('in range')
  })

  it('clearBookmark 清除书签', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    store.clearBookmark('p1')
    expect(store.getBookmark('p1')).toBeNull()
  })

  it('startSyncBookmark 保存同步面板对应的 serviceId', () => {
    const store = useBookmarkStore()
    store.syncPanelIds.add('p1')
    store.startSyncBookmark([{ panelId: 'p1', serviceId: 'svc-1' }])
    expect(store.getBookmark('p1')?.serviceId).toBe('svc-1')
  })

  it('endSyncBookmark 使用每个面板的快照冻结日志', () => {
    const store = useBookmarkStore()
    store.syncPanelIds.add('p1')
    store.startSyncBookmark([{ panelId: 'p1', serviceId: 'svc-1' }])
    const start = store.getBookmark('p1')!.startTime!
    vi.advanceTimersByTime(5000)
    const ts = new Date(start.getTime() + 1000).toISOString()
    const captured = makeLog('sync-visible-log', ts)

    store.endSyncBookmark([
      {
        panelId: 'p1',
        captureLogs: [captured],
        capturedIds: new Set([captured.id]),
      },
    ])

    const done = store.getBookmark('p1')!
    expect(done.state).toBe('done')
    expect(done.lockedLogs.map(l => l.message)).toEqual(['sync-visible-log'])
  })
})
