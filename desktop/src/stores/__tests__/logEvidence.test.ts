/**
 * logEvidence store 测试
 *
 * 职责：
 *   - 验证日志证据钉子的会话态模型
 *   - 验证分栏轨道、备注、导出范围、时间同步和候选钉子规则
 *
 * 边界：
 *   - 不测试具体 Vue 组件渲染
 *   - 不访问真实剪贴板或文件系统
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useLogEvidenceStore, type EvidenceTrackRegistration } from '../logEvidence'
import type { LogEntry } from '@/api/agent'

function makeLog(id: string, deploymentId: string, timestamp: string, message = `log-${id}`): LogEntry {
  return {
    id,
    deployment_id: deploymentId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

function makeTrack(trackId: string, label: string, logs: LogEntry[] = []): EvidenceTrackRegistration {
  return {
    trackId,
    panelId: trackId,
    trackLabel: label,
    sourceKey: label,
    getLogs: () => logs,
    jumpToLog: vi.fn(),
    alignToTime: vi.fn(),
  }
}

describe('useLogEvidenceStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('adds a pin snapshot with global sequence and color', () => {
    const store = useLogEvidenceStore()
    const log = makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z')

    const pin = store.addPin({
      panelId: 'panel-api',
      trackId: 'panel-api',
      trackLabel: 'api · dev',
      sourceKey: 'dep-api',
      log,
    })

    expect(pin.label).toBe('P1')
    expect(pin.color).toBe('#58a6ff')
    expect(pin.log).not.toBe(log)
    expect(store.pins).toHaveLength(1)
  })

  it('toggle removes an existing pin only inside the same track/source/log identity', () => {
    const store = useLogEvidenceStore()
    const log = makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z')

    store.togglePin({ panelId: 'panel-api', trackId: 'panel-api', trackLabel: 'api · dev', sourceKey: 'dep-api', log })
    const result = store.togglePin({ panelId: 'panel-api', trackId: 'panel-api', trackLabel: 'api · dev', sourceKey: 'dep-api', log })

    expect(result.action).toBe('removed')
    expect(store.pins).toHaveLength(0)
  })

  it('allows the same underlying log to be pinned in another track', () => {
    const store = useLogEvidenceStore()
    const log = makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z')

    store.addPin({ panelId: 'panel-a', trackId: 'panel-a', trackLabel: 'api left', sourceKey: 'dep-api', log })
    store.addPin({ panelId: 'panel-b', trackId: 'panel-b', trackLabel: 'api right', sourceKey: 'dep-api', log })

    expect(store.pins.map(pin => pin.label)).toEqual(['P1', 'P2'])
  })

  it('assigns the next label from the current maximum existing pin sequence', () => {
    const store = useLogEvidenceStore()
    const log = makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z')

    const first = store.addPin({ panelId: 'panel-a', trackId: 'panel-a', trackLabel: 'api', sourceKey: 'dep-api', log })
    store.removePin(first.id)
    const second = store.addPin({ panelId: 'panel-a', trackId: 'panel-a', trackLabel: 'api', sourceKey: 'dep-api', log })

    expect(second.label).toBe('P1')
  })

  it('updates notes and includes them in pinned Markdown', () => {
    const store = useLogEvidenceStore()
    const pin = store.addPin({
      panelId: 'panel-api',
      trackId: 'panel-api',
      trackLabel: 'api · dev',
      sourceKey: 'dep-api',
      log: makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z', 'retry'),
    })

    store.updateNote(pin.id, '怀疑 retry 从这里开始')

    expect(store.pins[0].note).toBe('怀疑 retry 从这里开始')
    expect(store.formatPinnedLinesMarkdown()).toContain('- note: 怀疑 retry 从这里开始')
  })

  it('filters evidence scope by current, selected, or all tracks', () => {
    const store = useLogEvidenceStore()
    store.addPin({ panelId: 'api', trackId: 'api', trackLabel: 'api', sourceKey: 'dep-api', log: makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z') })
    store.addPin({ panelId: 'worker', trackId: 'worker', trackLabel: 'worker', sourceKey: 'dep-worker', log: makeLog('2', 'dep-worker', '2026-06-20T10:00:01.000Z') })

    store.setEvidenceScope('current', 'api')
    expect(store.scopedPins.map(pin => pin.trackId)).toEqual(['api'])

    store.setSelectedTrackIds(['worker'])
    store.setEvidenceScope('selected', 'api')
    expect(store.scopedPins.map(pin => pin.trackId)).toEqual(['worker'])

    store.setEvidenceScope('all', 'api')
    expect(store.scopedPins.map(pin => pin.trackId)).toEqual(['api', 'worker'])
  })

  it('builds same-track segments and omitted gaps for selected scope', () => {
    const store = useLogEvidenceStore()
    const first = store.addPin({ panelId: 'api', trackId: 'api', trackLabel: 'api', sourceKey: 'dep-api', log: makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z') })
    const second = store.addPin({ panelId: 'api', trackId: 'api', trackLabel: 'api', sourceKey: 'dep-api', log: makeLog('2', 'dep-api', '2026-06-20T10:00:02.000Z') })
    store.addPin({ panelId: 'worker', trackId: 'worker', trackLabel: 'worker', sourceKey: 'dep-worker', log: makeLog('3', 'dep-worker', '2026-06-20T10:00:01.000Z') })
    store.registerTrack(makeTrack('api', 'api', [first.log, second.log]))
    store.toggleSegmentSkipped('api:1:2')

    const markdown = store.formatEvidencePackageMarkdown()

    expect(markdown).toContain('### Omitted P1 -> P2')
    expect(markdown).toContain('## Timeline')
    expect(markdown).toContain('P3 worker')
  })

  it('aligns sibling tracks by cursor time when time sync is enabled', () => {
    const store = useLogEvidenceStore()
    const apiTrack = makeTrack('api', 'api')
    const workerTrack = makeTrack('worker', 'worker')
    store.registerTrack(apiTrack)
    store.registerTrack(workerTrack)
    store.setTimeSyncEnabled(true)

    store.alignOtherTracksToLog('api', makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z'))

    expect(workerTrack.alignToTime).toHaveBeenCalledWith('2026-06-20T10:00:00.000Z', '1')
    expect(apiTrack.alignToTime).not.toHaveBeenCalled()
  })

  it('suggests same-time candidates from sibling tracks only', () => {
    const store = useLogEvidenceStore()
    const apiPin = store.addPin({
      panelId: 'api',
      trackId: 'api',
      trackLabel: 'api',
      sourceKey: 'dep-api',
      log: makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z'),
    })
    store.registerTrack(makeTrack('worker', 'worker', [
      makeLog('10', 'dep-worker', '2026-06-20T10:00:01.000Z', 'near'),
      makeLog('11', 'dep-worker', '2026-06-20T10:00:30.000Z', 'far'),
    ]))

    expect(store.sameTimeCandidatesForPin(apiPin.id).map(candidate => candidate.log.id)).toEqual(['10'])
  })
})
