/**
 * StepTree 测试覆盖运行控制台步骤/主机导航。
 *
 * 职责：
 *   - 验证步骤和主机任务渲染
 *   - 验证选择事件参数
 *
 * 边界：
 *   - 不加载 run 详情或日志
 */
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { StepRun } from '@/api/agent'
import StepTree from '../StepTree.vue'

const steps: StepRun[] = [{
  step_name: 'Deploy',
  type: 'local_command',
  phase: 'deploy',
  status: 'running',
  tasks: [
    { host_id: 'host-1', host_name: 'one', status: 'success' },
    { host_id: 'host-2', host_name: 'two', status: 'running' },
  ],
}]

afterEach(() => {
  vi.useRealTimers()
})

describe('StepTree', () => {
  it('renders step and host children and emits selection', async () => {
    const wrapper = mount(StepTree, { props: { steps, selectedStep: '', selectedHost: '' } })

    expect(wrapper.text()).toContain('Deploy')
    expect(wrapper.text()).toContain('one')
    await wrapper.find('[data-test="step-select"]').trigger('click')
    await wrapper.find('[data-test="host-select-host-1"]').trigger('click')

    expect(wrapper.emitted('select-step')?.[0]).toEqual(['Deploy'])
    expect(wrapper.emitted('select-host')?.[0]).toEqual(['Deploy', 'host-1'])
  })

  it('renders stable status icon classes', () => {
    const wrapper = mount(StepTree, { props: { steps, selectedStep: '', selectedHost: '' } })

    expect(wrapper.find('.status-icon.running').exists()).toBe(true)
    expect(wrapper.find('.status-icon.success').exists()).toBe(true)
  })

  it('renders finished step and host durations', () => {
    const timedSteps: StepRun[] = [{
      step_name: 'Deploy',
      type: 'remote_command',
      phase: 'deploy',
      status: 'success',
      tasks: [
        { host_id: 'host-1', host_name: 'one', status: 'success', started_at: 1_000, finished_at: 3_500 },
        { host_id: 'host-2', host_name: 'two', status: 'success', started_at: 2_000, finished_at: 6_500 },
      ],
    }]

    const wrapper = mount(StepTree, { props: { steps: timedSteps, selectedStep: '', selectedHost: '' } })

    expect(wrapper.find('[data-test="step-duration"]').text()).toBe('5s')
    expect(wrapper.findAll('[data-test="host-duration"]').map(item => item.text())).toEqual(['2s', '4s'])
  })

  it('updates running durations from the current clock', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(10_000)
    const runningSteps: StepRun[] = [{
      step_name: 'Deploy',
      type: 'remote_command',
      phase: 'deploy',
      status: 'running',
      tasks: [
        { host_id: 'host-1', host_name: 'one', status: 'running', started_at: 8_000 },
      ],
    }]

    const wrapper = mount(StepTree, { props: { steps: runningSteps, selectedStep: '', selectedHost: '' } })

    expect(wrapper.find('[data-test="host-duration"]').text()).toBe('2s')
    vi.setSystemTime(11_000)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(wrapper.find('[data-test="host-duration"]').text()).toBe('4s')
  })
})
