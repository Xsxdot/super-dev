import { describe, it, expect } from 'vitest'
import {
  normalize,
  ingest,
  closeActiveFold,
  toDisplayEntry,
  type DisplayLogEntry,
} from '../logEngine'
import type { LogEntry } from '@/api/agent'

function makeLog(message: string, serviceId = 'svc-a'): LogEntry {
  return {
    id: Math.floor(Math.random() * 1e6),
    deployment_id: serviceId,
    run_id: 'run',
    timestamp: new Date().toISOString(),
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('logEngine', () => {
  it('normalize strips time prefix', () => {
    const a = normalize('10:23:01 ERROR boom')
    const b = normalize('10:24:02 ERROR boom')
    expect(a).toBe(b)
  })

  it('normalize strips numeric ids', () => {
    expect(normalize('uid=42 ok')).toBe(normalize('uid=99 ok'))
  })

  it('normalize strips weekday date prefix', () => {
    const a = normalize('Wed May 20 17:20:51 CST 2026 INFO heartbeat')
    const b = normalize('Wed May 20 17:20:52 CST 2026 INFO heartbeat')
    expect(a).toBe(b)
  })

  it('ingest folds heartbeat with weekday date in message', () => {
    const entries: DisplayLogEntry[] = []
    ingest(
      toDisplayEntry(makeLog('Wed May 20 17:20:51 CST 2026 INFO heartbeat')),
      entries,
    )
    ingest(
      toDisplayEntry(makeLog('Wed May 20 17:20:52 CST 2026 INFO heartbeat')),
      entries,
    )
    expect(entries).toHaveLength(1)
    expect(entries[0].repeat_count).toBe(2)
  })

  it('ingest folds adjacent duplicates', () => {
    const entries: DisplayLogEntry[] = []
    ingest(toDisplayEntry(makeLog('heartbeat ok')), entries)
    ingest(toDisplayEntry(makeLog('heartbeat ok')), entries)
    expect(entries).toHaveLength(1)
    expect(entries[0].repeat_count).toBe(2)
  })

  it('toDisplayEntry assigns unique ids to live logs without database ids', () => {
    const first = toDisplayEntry({ ...makeLog('first live'), id: 0 })
    const second = toDisplayEntry({ ...makeLog('second live'), id: 0 })

    expect(first.id).not.toBe(0)
    expect(second.id).not.toBe(0)
    expect(first.id).not.toBe(second.id)
  })

  it('ingest does not fold different services', () => {
    const entries: DisplayLogEntry[] = []
    ingest(toDisplayEntry(makeLog('same msg', 'a')), entries)
    ingest(toDisplayEntry(makeLog('same msg', 'b')), entries)
    expect(entries).toHaveLength(2)
  })

  it('closeActiveFold makes the next duplicate start a new folded row', () => {
    const entries: DisplayLogEntry[] = []
    ingest(toDisplayEntry(makeLog('heartbeat ok')), entries)
    ingest(toDisplayEntry(makeLog('heartbeat ok')), entries)

    closeActiveFold(entries)
    ingest(toDisplayEntry(makeLog('heartbeat ok')), entries)
    ingest(toDisplayEntry(makeLog('heartbeat ok')), entries)

    expect(entries.map(e => e.repeat_count)).toEqual([2, 2])
  })
})
