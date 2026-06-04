/**
 * WorkspaceShell Project Overview 渲染测试。
 *
 * 职责：
 *   - 验证 overview workspace tab 渲染项目概览内容
 *
 * 边界：
 *   - ProjectOverviewPane 使用轻量 stub
 *   - 不渲染真实运行状态、流水线或 Ingress 子页面
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkspaceShell from '../WorkspaceShell.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/components/Overview/ProjectOverviewPane.vue', () => ({
  default: {
    props: ['project'],
    template: '<section data-test="overview-pane">{{ project.name }}</section>',
  },
}))

describe('WorkspaceShell overview tab', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('renders project overview as a workspace tab', () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [],
    }]
    useWorkspaceStore().openProjectOverview('proj-1')

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    expect(wrapper.find('[data-test="overview-pane"]').text()).toContain('Demo')
  })
})
