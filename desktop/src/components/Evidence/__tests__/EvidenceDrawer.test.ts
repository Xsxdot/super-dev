/**
 * EvidenceDrawer 组件测试
 *
 * 职责：
 *   - 验证证据抽屉的 Timeline、scope、segments、preview、复制和导出行为
 *   - 验证跨分栏 pins 只进入 Timeline，同轨道 pins 才形成区间
 *
 * 边界：
 *   - 不打开真实系统文件对话框
 *   - 不测试 LogPanel 的滚动实现
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import EvidenceDrawer from '../EvidenceDrawer.vue'
import EvidenceSegmentList from '../EvidenceSegmentList.vue'
import { useLogEvidenceStore, type EvidenceTrackRegistration } from '@/stores/logEvidence'
import { installTestI18n } from '@/test-utils/i18n'
import type { LogEntry } from '@/api/agent'

const tauriMocks = vi.hoisted(() => ({
  save: vi.fn(),
  writeTextFile: vi.fn(),
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  save: tauriMocks.save,
}))

vi.mock('@tauri-apps/plugin-fs', () => ({
  writeTextFile: tauriMocks.writeTextFile,
}))

function makeLog(id: string, deploymentId: string, timestamp: string, message: string): LogEntry {
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

function makeTrack(trackId: string, label: string, logs: LogEntry[], jumpToLog = vi.fn()): EvidenceTrackRegistration {
  return {
    trackId,
    panelId: trackId,
    trackLabel: label,
    sourceKey: logs[0]?.deployment_id ?? trackId,
    getLogs: () => logs,
    jumpToLog,
    alignToTime: vi.fn(),
  }
}

function seedEvidenceStore() {
  const store = useLogEvidenceStore()
  const apiFirst = makeLog('1', 'dep-api', '2026-06-20T10:00:00.000Z', 'api first')
  const worker = makeLog('2', 'dep-worker', '2026-06-20T10:00:01.000Z', 'worker near')
  const apiSecond = makeLog('3', 'dep-api', '2026-06-20T10:00:02.000Z', 'api second')
  const workerCandidate = makeLog('10', 'dep-worker', '2026-06-20T10:00:00.900Z', 'worker candidate')
  const apiJump = vi.fn()
  const workerJump = vi.fn()

  store.registerTrack(makeTrack('api', 'api · dev', [apiFirst, apiSecond], apiJump))
  store.registerTrack(makeTrack('worker', 'worker · dev', [workerCandidate, worker], workerJump))
  const p1 = store.addPin({ panelId: 'api', trackId: 'api', trackLabel: 'api · dev', sourceKey: 'dep-api', log: apiFirst })
  const p2 = store.addPin({ panelId: 'worker', trackId: 'worker', trackLabel: 'worker · dev', sourceKey: 'dep-worker', log: worker })
  const p3 = store.addPin({ panelId: 'api', trackId: 'api', trackLabel: 'api · dev', sourceKey: 'dep-api', log: apiSecond })
  store.updateNote(p1.id, 'retry starts')

  return { store, p1, p2, p3, apiJump, workerJump }
}

function mountDrawer() {
  return mount(EvidenceDrawer, {
    global: {
      plugins: [installTestI18n('en-US')],
    },
  })
}

describe('EvidenceDrawer', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    tauriMocks.save.mockResolvedValue('/tmp/evidence.md')
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn(),
      },
    })
  })

  it('shows evidence count and Timeline pins sorted across tracks', () => {
    seedEvidenceStore()

    const wrapper = mountDrawer()

    expect(wrapper.find('[data-test="evidence-drawer"]').text()).toContain('Evidence')
    expect(wrapper.find('[data-test="evidence-count"]').text()).toContain('3')
    expect(wrapper.findAll('[data-test="evidence-timeline-item"]').map(item => item.text())).toEqual([
      expect.stringContaining('P1'),
      expect.stringContaining('P2'),
      expect.stringContaining('P3'),
    ])
  })

  it('clicking a timeline item jumps to the owning pin', async () => {
    const { apiJump } = seedEvidenceStore()
    const wrapper = mountDrawer()

    await wrapper.findAll('[data-test="evidence-timeline-item"]')[0].trigger('click')

    expect(apiJump).toHaveBeenCalledWith('1')
  })

  it('can switch scope to selected tracks', async () => {
    const { store } = seedEvidenceStore()
    const wrapper = mountDrawer()

    await wrapper.find('[data-test="scope-selected"]').trigger('click')
    await wrapper.find('[data-test="track-worker"]').setValue(true)

    expect(store.scopeMode).toBe('selected')
    expect(store.scopedPins.map(pin => pin.trackId)).toEqual(['worker'])
  })

  it('segments only connect same-track pins and can be skipped in preview', async () => {
    const { store } = seedEvidenceStore()
    const wrapper = mountDrawer()

    await wrapper.find('[data-test="tab-segments"]').trigger('click')
    await Promise.resolve()
    await nextTick()
    expect(wrapper.find('[data-test="segment-list"]').text()).toContain('P1 -> P3')
    expect(wrapper.find('[data-test="segment-list"]').text()).not.toContain('P1 -> P2')

    const segment = store.exportModel().tracks.find(track => track.trackId === 'api')?.segments[0]
    expect(segment?.key).toBeTruthy()
    store.toggleSegmentSkipped(segment!.key)
    expect(store.skippedSegmentKeys.size).toBe(1)
    await wrapper.find('[data-test="tab-preview"]').trigger('click')

    expect(wrapper.find('[data-test="evidence-preview"]').text()).toContain('Omitted P1 -> P3')
  })

  it('segment list toggles skipped segments directly', async () => {
    const { store } = seedEvidenceStore()
    const toggleSegmentSkipped = vi.spyOn(store, 'toggleSegmentSkipped')
    const wrapper = mount(EvidenceSegmentList, {
      global: {
        plugins: [installTestI18n('en-US')],
      },
    })

    await wrapper.find('[data-test="segment-skip"]').trigger('click')

    expect(toggleSegmentSkipped).toHaveBeenCalledTimes(1)
    expect(store.skippedSegmentKeys.size).toBe(1)
  })

  it('renders same-time candidates and can pin one explicitly', async () => {
    const { store } = seedEvidenceStore()
    const wrapper = mountDrawer()

    await wrapper.find('[data-test="same-time-candidate-pin"]').trigger('click')

    expect(store.pins.some(pin => pin.logId === '10' && pin.trackId === 'worker')).toBe(true)
  })

  it('copies and exports Markdown evidence', async () => {
    seedEvidenceStore()
    const wrapper = mountDrawer()

    await wrapper.find('[data-test="copy-evidence"]').trigger('click')
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('# SuperDev Log Evidence'))

    await wrapper.find('[data-test="export-evidence"]').trigger('click')
    await nextTick()

    expect(tauriMocks.save).toHaveBeenCalled()
    expect(tauriMocks.writeTextFile).toHaveBeenCalledWith('/tmp/evidence.md', expect.stringContaining('cursor_id'))
  })

  it('shows immediate feedback after copying Markdown evidence', async () => {
    seedEvidenceStore()
    const wrapper = mountDrawer()

    await wrapper.find('[data-test="copy-evidence"]').trigger('click')
    await nextTick()

    const copyButton = wrapper.find('[data-test="copy-evidence"]')
    expect(copyButton.text()).toContain('Copied')
    expect(copyButton.classes()).toContain('feedback-success')
  })

  it('has an explicit close button for the floating drawer', async () => {
    const { store } = seedEvidenceStore()
    store.setDrawerOpen(true)
    const wrapper = mountDrawer()

    await wrapper.find('[data-test="close-evidence"]').trigger('click')

    expect(store.drawerOpen).toBe(false)
  })

  it('renders clear evidence as a text button instead of a fixed-width icon button', () => {
    seedEvidenceStore()
    const wrapper = mountDrawer()
    const clearButton = wrapper.find('[data-test="clear-evidence"]')

    expect(clearButton.classes()).toContain('action-btn')
    expect(clearButton.classes()).not.toContain('icon-btn')
  })
})
