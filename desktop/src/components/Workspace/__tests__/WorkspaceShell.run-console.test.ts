/**
 * WorkspaceShell run console tab 渲染测试。
 *
 * 职责：
 *   - 验证 workspace run tab 会渲染 RunConsolePage
 *   - 验证 run tab 参数会透传给控制台页面
 *
 * 边界：
 *   - RunConsolePage 使用轻量 stub
 *   - 不建立真实 WebSocket 或加载 run 数据
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkspaceShell from '../WorkspaceShell.vue'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/components/Overview/RunConsole/RunConsolePage.vue', () => ({
  default: {
    props: ['projectId', 'pipelineId', 'runId', 'mode'],
    template: '<div data-test="run-console-page">{{ projectId }} {{ pipelineId }} {{ runId }} {{ mode }}</div>',
  },
}))

describe('WorkspaceShell run console tab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders RunConsolePage for run workspace tab', () => {
    const workspace = useWorkspaceStore()
    workspace.openRunConsole({
      projectId: 'p1',
      pipelineId: 'deploy',
      runId: 'run-1',
      mode: 'live',
      title: 'Deploy · run-1',
    })

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="run-console-page"]').text()).toContain('p1 deploy run-1 live')
  })
})
