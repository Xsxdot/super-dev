/**
 * WorkspaceShell.remote 测试远程 workspace tab 的渲染分支。
 *
 * 职责：
 *   - 验证 remote tab 渲染远程 LogPanel
 *
 * 边界：
 *   - 不测试 LogPanel 内部日志行为
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
import { useWorkspaceStore } from '@/stores/workspace'

vi.mock('@/components/Panel/LogPanel.vue', () => ({
  default: {
    props: ['logSourceId', 'groupKey'],
    template: '<div data-test="remote-log-panel">{{ logSourceId }}:{{ groupKey }}</div>',
  },
}))

describe('WorkspaceShell remote tab', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('remote tab 渲染远程 LogPanel', () => {
    const workspace = useWorkspaceStore()
    workspace.openRemote('ls1', 'prod')

    const wrapper = mount(WorkspaceShell)

    expect(wrapper.find('[data-test="remote-log-panel"]').text()).toBe('ls1:prod')
  })
})
