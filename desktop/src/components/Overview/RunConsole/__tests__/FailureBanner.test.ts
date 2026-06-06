/**
 * FailureBanner 测试覆盖运行失败诊断横幅。
 *
 * 职责：
 *   - 验证首个 failed task 的 step、host alias、exit code 展示
 *   - 验证查看日志事件参数
 *
 * 边界：
 *   - 不加载 run 数据
 *   - 不验证 HostLogPanel 渲染
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { Run } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'
import FailureBanner from '../FailureBanner.vue'

function failedRun(): Run {
  return {
    id: 'run-1',
    deployment_id: 'dep-1',
    status: 'failed',
    started_at: 1,
    step_runs: [{
      step_name: 'Deploy API',
      type: 'remote_command',
      phase: 'deploy',
      status: 'failed',
      tasks: [{ host_id: 'host-uuid', host_name: 'local-01', status: 'failed', exit_code: 42 }],
    }],
  }
}

describe('FailureBanner', () => {
  it('renders failed task details and emits log jump target', async () => {
    const wrapper = mount(FailureBanner, {
      props: { run: failedRun() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('Deploy API')
    expect(wrapper.text()).toContain('local-01')
    expect(wrapper.text()).toContain('exit 42')

    await wrapper.find('[data-test="view-failure-logs"]').trigger('click')
    expect(wrapper.emitted('view-logs')?.[0]).toEqual(['Deploy API', 'host-uuid'])
  })

  it('renders nothing when there is no failed task', () => {
    const run = failedRun()
    run.status = 'success'
    run.step_runs[0].status = 'success'
    run.step_runs[0].tasks[0].status = 'success'
    const wrapper = mount(FailureBanner, {
      props: { run },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.html()).toBe('<!--v-if-->')
  })
})
