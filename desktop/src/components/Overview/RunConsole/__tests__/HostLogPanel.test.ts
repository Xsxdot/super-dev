/**
 * HostLogPanel 测试覆盖运行控制台日志渲染。
 *
 * 职责：
 *   - 验证 RunLogLine 到显示日志的转换
 *   - 验证 step/host 范围过滤
 *
 * 边界：
 *   - 不验证日志规则编辑或 WebSocket 订阅
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { RunLogLine } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'
import HostLogPanel from '../HostLogPanel.vue'

const logs: RunLogLine[] = [
  { id: 1, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stdout', line: 'one', at: 1000 },
  { id: 2, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-2', stream: 'stdout', line: 'two', at: 1001 },
]

describe('HostLogPanel', () => {
  it('filters logs by host id', () => {
    const wrapper = mount(HostLogPanel, {
      props: { logs, selectedStep: 'Deploy', selectedHost: 'host-1' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('one')
    expect(wrapper.text()).not.toContain('two')
  })

  it('marks stderr rows and shows waiting copy while running without logs', () => {
    const wrapper = mount(HostLogPanel, {
      props: {
        logs: [],
        selectedStep: '',
        selectedHost: '',
        loading: false,
        running: true,
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('Waiting for output')
  })

  it('uses stderr class for stderr rows', () => {
    const wrapper = mount(HostLogPanel, {
      props: {
        logs: [{ id: 3, run_id: 'run-1', step_name: 'Deploy', host_id: 'host-1', stream: 'stderr', line: 'bad', at: 1002 }],
        selectedStep: '',
        selectedHost: '',
        loading: false,
        running: false,
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('.run-log-row.stderr').exists()).toBe(true)
  })
})
