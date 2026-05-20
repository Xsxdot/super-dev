import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { useBookmarkStore } from '../bookmark'
import type { LogEntry } from '@/api/agent'

function makeLog(message: string, ts: string): LogEntry {
  return { id: 1, service_id: 'svc', run_id: 'run', timestamp: ts, level: 'INFO', message, stream: 'stdout' }
}

describe('bookmarkStore', () => {
  beforeEach(() => setActivePinia(createPinia()))

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

  it('appendToBookmark 追加录制期间的日志', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    const startTs = store.getBookmark('p1')!.startTime!
    const afterTs = new Date(startTs.getTime() + 1000).toISOString()
    store.appendToBookmark('p1', makeLog('hello', afterTs))
    expect(store.getBookmark('p1')?.lockedLogs).toHaveLength(1)
  })

  it('clearBookmark 清除书签', () => {
    const store = useBookmarkStore()
    store.startBookmark('p1', 'svc-1')
    store.clearBookmark('p1')
    expect(store.getBookmark('p1')).toBeNull()
  })
})
