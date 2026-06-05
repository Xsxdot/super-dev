/**
 * SearchServiceRail 组件测试。
 *
 * 职责：
 *   - 验证搜索命中服务摘要按服务展示数量和分布提示
 *
 * 边界：
 *   - 不验证真实颜色像素
 *   - 不请求真实 agent API
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SearchServiceRail from '../SearchServiceRail.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: 'running',
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

describe('SearchServiceRail', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('渲染两服务命中摘要和分布条', () => {
    const api = service('sample-api-demo', 'sample-api-demo')
    const worker = service('sample-worker-demo', 'sample-worker-demo')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'sample-api-demo': 43, 'sample-worker-demo': 33 }

    const wrapper = mount(SearchServiceRail, {
      props: { tabId: tab.id },
    })

    const rows = wrapper.findAll('[data-test="matched-service-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('sample-api-demo')
    expect(rows[0].text()).toContain('43')
    expect(rows[1].text()).toContain('sample-worker-demo')
    expect(rows[1].text()).toContain('33')
    expect(wrapper.findAll('[data-test="service-distribution-bar"]')).toHaveLength(2)
  })
})
