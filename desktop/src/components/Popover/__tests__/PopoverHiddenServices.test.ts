/**
 * Popover hidden services 测试本地显示/隐藏偏好。
 *
 * 职责：
 *   - 验证 popover 左侧项目服务列表隐藏被设置页隐藏的服务
 *   - 验证 popover 右侧服务控制面板隐藏被设置页隐藏的服务
 *
 * 边界：
 *   - 不测试真实托盘窗口
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import PopoverProjectList from '../PopoverProjectList.vue'
import PopoverServicePanel from '../PopoverServicePanel.vue'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: '',
    required: false,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    env_selected_service_ids: {},
  }
}

describe('popover hidden services', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('隐藏服务不出现在 popover 列表和控制面板', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    const agent = useAgentStore()
    agent.projects = [project([api, worker])]
    const settings = useSettingsStore()
    settings.toggleServiceHidden('svc-worker')

    const list = mount(PopoverProjectList)
    const panel = mount(PopoverServicePanel, { props: { project: agent.projects[0] } })

    expect(list.text()).toContain('api')
    expect(list.text()).not.toContain('worker')
    expect(panel.text()).toContain('api')
    expect(panel.text()).not.toContain('worker')
  })
})
