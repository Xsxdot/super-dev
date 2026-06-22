import { describe, it, expect } from 'vitest'
import { applyEvent, toDisplayEntry, type DisplayLogEntry } from '../logEngine'

const base = (over: Partial<DisplayLogEntry> = {}): DisplayLogEntry =>
  toDisplayEntry({
    id: '1',
    deployment_id: 'A',
    run_id: 'r',
    timestamp: '2026-06-08T12:00:00Z',
    level: 'INFO',
    message: 'boom',
    stream: 'stdout',
    repeat_count: 1,
    fold_key: 'k1',
    ...over,
  })

describe('logEngine without client-side folding', () => {
  it('renders backend repeat_count directly', () => {
    const entry = base({ repeat_count: 3 })
    expect(entry.repeat_count).toBe(3)
  })

  it('assigns unique ids to live logs without database ids', () => {
    const first = base({ id: '0', message: 'first live' })
    const second = base({ id: '0', message: 'second live' })

    expect(first.id).not.toBe('0')
    expect(second.id).not.toBe('0')
    expect(first.id).not.toBe(second.id)
  })

  it('keeps live log id stable for the same source payload', () => {
    const first = base({ id: '0', source_id: 'node-a', message: 'same live' })
    const second = base({ id: '0', source_id: 'node-a', message: 'same live' })

    expect(first.id).toBe(second.id)
  })

  it('prefers source-scoped row id when database id exists', () => {
    const entry = base({ id: '42', source_id: 'node-x' })
    expect(entry.id).toBe('node-x:42')
  })

  it('preserves backend row id as MCP cursor id when display id is source-scoped', () => {
    const entry = base({ id: '218895', source_id: 'superdev-7d96' })
    expect(entry.id).toBe('superdev-7d96:218895')
    expect(entry.cursor_id).toBe('218895')
  })

  it('appends a new entry event', () => {
    const list: DisplayLogEntry[] = []
    applyEvent(list, { entry: base({ id: '1', fold_key: 'k1', repeat_count: 1 }) })
    expect(list).toHaveLength(1)
    expect(list[0].repeat_count).toBe(1)
  })

  it('updates existing row count on increment event by fold_key', () => {
    const list: DisplayLogEntry[] = []
    applyEvent(list, { entry: base({ id: '1', fold_key: 'k1', repeat_count: 1 }) })
    applyEvent(list, { increment: { deployment_id: 'A', fold_key: 'k1', repeat_count: 4 } })
    expect(list).toHaveLength(1)
    expect(list[0].repeat_count).toBe(4)
  })

  it('ignores increment with unknown fold_key', () => {
    const list: DisplayLogEntry[] = []
    applyEvent(list, { entry: base({ id: '1', fold_key: 'k1' }) })
    applyEvent(list, { increment: { deployment_id: 'A', fold_key: 'zzz', repeat_count: 9 } })
    expect(list[0].repeat_count).toBe(1)
  })
})
