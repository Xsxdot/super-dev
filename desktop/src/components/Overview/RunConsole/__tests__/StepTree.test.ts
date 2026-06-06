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
import { describe, expect, it } from 'vitest'
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
})
