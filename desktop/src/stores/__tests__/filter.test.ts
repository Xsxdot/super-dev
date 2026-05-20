import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { useFilterStore } from '../filter'
import type { LogEntry } from '@/api/agent'

function makeLog(message: string, id = 1): LogEntry {
  return { id, service_id: 'svc', run_id: 'run', timestamp: '', level: 'INFO', message, stream: 'stdout' }
}

describe('filterStore', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('addChip 添加不重复的 chip', () => {
    const store = useFilterStore()
    store.addChip('p1', 'error', 'include')
    store.addChip('p1', 'error', 'include')  // 重复，不添加
    expect(store.getPanel('p1').chips).toHaveLength(1)
  })

  it('applyFilters：include chip 过滤', () => {
    const store = useFilterStore()
    store.addChip('p1', 'error', 'include')
    const logs = [makeLog('error occurred'), makeLog('info message')]
    const result = store.applyFilters('p1', null, logs)
    expect(result).toHaveLength(1)
    expect(result[0].message).toBe('error occurred')
  })

  it('applyFilters：exclude chip 过滤', () => {
    const store = useFilterStore()
    store.addChip('p1', 'debug', 'exclude')
    const logs = [makeLog('debug info'), makeLog('error occurred')]
    const result = store.applyFilters('p1', null, logs)
    expect(result).toHaveLength(1)
    expect(result[0].message).toBe('error occurred')
  })
})
