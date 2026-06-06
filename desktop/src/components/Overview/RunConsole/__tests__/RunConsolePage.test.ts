/**
 * RunConsolePage 测试覆盖运行控制台页面编排。
 *
 * 职责：
 *   - 验证失败横幅的查看日志事件会调用 runConsole store selection
 *
 * 边界：
 *   - StepTree 和 HostLogPanel 使用轻量 stub
 *   - 不建立真实 WebSocket
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { installTestI18n } from '@/test-utils/i18n'
import RunConsolePage from '../RunConsolePage.vue'

const storeMock = vi.hoisted(() => ({
  select: vi.fn(),
  loadLive: vi.fn(),
  loadReplay: vi.fn(),
  visibleLogs: vi.fn(() => []),
  state: {
    currentRun: {
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
    },
    selectedStep: '',
    selectedHost: '',
    loading: false,
    error: null,
  },
}))

vi.mock('@/stores/runConsole', () => ({
  useRunConsoleStore: () => ({
    stateFor: () => storeMock.state,
    visibleLogs: storeMock.visibleLogs,
    select: storeMock.select,
    loadLive: storeMock.loadLive,
    loadReplay: storeMock.loadReplay,
  }),
}))

vi.mock('../StepTree.vue', () => ({
  default: { props: ['steps', 'selectedStep', 'selectedHost'], template: '<nav data-test="step-tree" />' },
}))

vi.mock('../HostLogPanel.vue', () => ({
  default: { props: ['logs', 'selectedStep', 'selectedHost', 'loading', 'running'], template: '<section data-test="host-log-panel" />' },
}))

describe('RunConsolePage', () => {
  it('selects failed step and host when FailureBanner asks to view logs', async () => {
    storeMock.select.mockClear()
    const wrapper = mount(RunConsolePage, {
      props: { projectId: 'p1', pipelineId: 'deploy', runId: 'run-1', mode: 'replay' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="view-failure-logs"]').trigger('click')

    expect(storeMock.select).toHaveBeenCalledWith('run-1', 'Deploy API', 'host-uuid')
  })
})
