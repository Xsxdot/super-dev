/**
 * LogRow 证据钉子测试
 *
 * 职责：
 *   - 验证日志行展示证据钉子的固定槽位
 *   - 验证双击、钉子点击和右键菜单事件
 *
 * 边界：
 *   - 不测试 LogPanel 如何处理事件
 *   - 不测试 note popover 的编辑流程
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import LogRow from '../LogRow.vue'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { EvidencePin } from '@/stores/logEvidence'

function makeLog(): DisplayLogEntry {
  return {
    id: '1849',
    deployment_id: 'dep-api',
    run_id: 'run-1',
    timestamp: '2026-06-20T10:32:14.000Z',
    level: 'WARN',
    message: 'retrying request',
    stream: 'stdout',
    repeat_count: 1,
  }
}

function makePin(log = makeLog()): EvidencePin {
  return {
    id: 'pin-1',
    panelId: 'panel-api',
    trackId: 'panel-api',
    trackLabel: 'api · dev',
    sourceKey: 'dep-api',
    sequence: 1,
    label: 'P1',
    color: '#58a6ff',
    logId: log.id,
    log,
    note: '第一次出现 retry',
    createdAt: '2026-06-20T10:32:20.000Z',
  }
}

function mountRow(evidencePin: EvidencePin | null = null) {
  return mount(LogRow, {
    props: {
      log: makeLog(),
      serviceName: 'api',
      highlighted: false,
      evidencePin,
      evidenceFlash: false,
      timeAnchor: false,
    },
    global: {
      stubs: {
        SelectableLogText: { props: ['text'], template: '<span class="selectable-msg">{{ text }}</span>' },
      },
    },
  })
}

describe('LogRow evidence pins', () => {
  it('renders a stable empty pin slot for unpinned rows', () => {
    const wrapper = mountRow()

    expect(wrapper.find('[data-test="log-pin-slot"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="log-pin-badge"]').exists()).toBe(false)
  })

  it('renders pin badge and note indicator when pinned', () => {
    const wrapper = mountRow(makePin())

    expect(wrapper.find('[data-test="log-pin-badge"]').text()).toContain('P1')
    expect(wrapper.find('[data-test="log-pin-note-indicator"]').exists()).toBe(true)
  })

  it('emits toggle-pin on row double click', async () => {
    const wrapper = mountRow()

    await wrapper.find('.log-row').trigger('dblclick')

    expect(wrapper.emitted('toggle-pin')?.[0]?.[0]).toMatchObject({ id: '1849' })
  })

  it('emits edit-pin when clicking the pin badge', async () => {
    const pin = makePin()
    const wrapper = mountRow(pin)

    await wrapper.find('[data-test="log-pin-badge"]').trigger('click')

    expect(wrapper.emitted('edit-pin')?.[0]?.[0]).toMatchObject({ id: pin.id })
    expect(wrapper.emitted('edit-pin')?.[0]?.[1]).toBeInstanceOf(MouseEvent)
  })

  it('emits row-context-menu with event and log on right click', async () => {
    const wrapper = mountRow()

    await wrapper.find('.log-row').trigger('contextmenu')

    const emitted = wrapper.emitted('row-context-menu')?.[0]
    expect(emitted?.[0]).toBeInstanceOf(MouseEvent)
    expect(emitted?.[1]).toMatchObject({ id: '1849' })
  })
})
