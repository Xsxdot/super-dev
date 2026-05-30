import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import type { Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      putProjectSetup: vi.fn().mockResolvedValue({}),
      listProjects: vi.fn().mockResolvedValue([]),
    },
  }
})

function project(): Project {
  return {
    id: 'p1', name: 'demo', root_path: '/tmp/demo', env_selected_service_ids: {},
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    services: [{ id: 's1', project_id: 'p1', name: 'web', status: '', required: false, order: 0, deployments: [] }],
  }
}

describe('ProjectConfigEditor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('渲染 env tab 与服务列表', async () => {
    const wrapper = mount(ProjectConfigEditor, { props: { project: project() } })
    await new Promise(r => setTimeout(r))
    expect(wrapper.find('[data-test="env-tab"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="service-card"]').exists()).toBe(true)
  })

  it('校验失败时阻止保存并展示错误', async () => {
    const { api } = await import('@/api/agent')
    const p = project()
    p.environments![0].name = ''
    const wrapper = mount(ProjectConfigEditor, { props: { project: p } })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-save"]').trigger('click')
    expect(api.putProjectSetup).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('环境名称不能为空')
  })

  it('点击取消 emit cancel', async () => {
    const wrapper = mount(ProjectConfigEditor, { props: { project: project() } })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-cancel"]').trigger('click')
    expect(wrapper.emitted('cancel')).toBeTruthy()
  })

  it('校验通过时保存：调用 putProjectSetup 并 emit saved', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mount(ProjectConfigEditor, { props: { project: project() } })
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))
    expect(api.putProjectSetup).toHaveBeenCalledTimes(1)
    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      environments: expect.any(Array),
      services: expect.any(Array),
    }))
    expect(wrapper.emitted('saved')).toBeTruthy()
  })
})
