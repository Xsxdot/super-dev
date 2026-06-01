/**
 * PopoverServicePanel i18n 测试托盘控制面板文案。
 *
 * 职责：
 *   - 验证英文 locale 下托盘服务控制文案来自 i18n
 *
 * 边界：
 *   - 不调用真实 Tauri 主窗口命令
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import PopoverServicePanel from '../PopoverServicePanel.vue'
import { useAgentStore } from '@/stores/agent'
import { installTestI18n } from '@/test-utils/i18n'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string, required = false): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: 'running',
    required,
    order: 0,
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

describe('PopoverServicePanel i18n', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('英文 locale 下渲染控制面板文案', () => {
    const p = project([service('svc-api', 'api', true), service('svc-worker', 'worker')])
    useAgentStore().projects = [p]

    const wrapper = mount(PopoverServicePanel, {
      props: { project: p },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('Stop All')
    expect(wrapper.text()).toContain('Start Selected')
    expect(wrapper.text()).toContain('Required')
    expect(wrapper.text()).toContain('Optional')
    expect(wrapper.text()).toContain('View Logs')
  })
})
