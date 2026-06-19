/**
 * PopoverServiceStatus 测试托盘服务状态来源。
 *
 * 职责：
 *   - 验证托盘控制面板按当前开发环境 deployment 状态渲染
 *   - 防止 legacy service.status 与 deployment.status 脱节时误显示停止
 *
 * 边界：
 *   - 不测试真实系统托盘窗口
 *   - 不调用真实 agent HTTP 接口
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import PopoverProjectList from '../PopoverProjectList.vue'
import PopoverServicePanel from '../PopoverServicePanel.vue'
import { useAgentStore } from '@/stores/agent'
import { installTestI18n } from '@/test-utils/i18n'
import type { Project, Service } from '@/api/agent'

function serviceWithDevDeployment(status: '' | 'starting' | 'running' | 'failed' = 'running'): Service {
  return {
    id: 'svc-api',
    project_id: 'proj-1',
    name: 'api',
    status: '',
    required: true,
    order: 0,
    deployments: [
      {
        id: 'dep-api-dev',
        env_name: 'dev',
        location: 'local',
        status,
      },
    ],
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    env_selected_service_ids: { dev: ['api'] },
    environments: [{ id: 'env-dev', name: 'dev', is_dev: true, order: 0 }],
  }
}

describe('PopoverServiceStatus', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('使用开发环境 deployment 状态渲染服务行和统计', () => {
    const p = project([serviceWithDevDeployment()])
    useAgentStore().projects = [p]

    const wrapper = mount(PopoverServicePanel, {
      props: { project: p },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('● 1 running')
    expect(wrapper.text()).toContain('Running')
    expect(wrapper.text()).not.toContain('● 1 stopped')
    expect(wrapper.text()).not.toContain('Not started')
  })

  it('使用开发环境 deployment 状态渲染左侧项目列表状态点', () => {
    const p = project([serviceWithDevDeployment()])
    useAgentStore().projects = [p]

    const wrapper = mount(PopoverProjectList, {
      global: { plugins: [installTestI18n('en-US')] },
    })

    const dotStyle = wrapper.find('.left-service-row .status-dot').attributes('style') ?? ''
    expect(dotStyle).toContain('rgb(63, 185, 80)')
  })

  it('把 starting 渲染为独立状态文案和样式类', () => {
    const p = project([serviceWithDevDeployment('starting')])
    useAgentStore().projects = [p]

    const wrapper = mount(PopoverServicePanel, {
      props: { project: p },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const label = wrapper.get('.status-label')
    expect(label.text()).toBe('Starting')
    expect(label.classes()).toContain('status-starting')
  })
})
