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

function projectWithDeployment(): Project {
  const p = project()
  p.services[0].deployments = [{ id: 'd1', env_name: 'dev', location: 'local', command: 'npm run dev', work_dir: '/tmp/demo/web', status: '' }]
  return p
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

  it('新启用服务环境时保存 runtime/logs 配置', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mount(ProjectConfigEditor, { props: { project: project() } })
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="enable-dep"]').trigger('click')
    await wrapper.find('[data-test="dep-command"]').setValue('npm run dev')
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      services: expect.arrayContaining([
        expect.objectContaining({
          deployments: expect.arrayContaining([
            expect.objectContaining({
              control_mode: 'managed',
              runtime: expect.objectContaining({ type: 'command', command: 'npm run dev' }),
              logs: expect.objectContaining({ type: 'process' }),
            }),
          ]),
        }),
      ]),
    }))
  })

  it('不再渲染项目级 pipeline 编辑区', async () => {
    const wrapper = mount(ProjectConfigEditor, { props: { project: projectWithDeployment() } })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="add-project-pipeline"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('项目流水线')
  })

  it('保存服务配置时保留已有项目级 pipeline', async () => {
    const { api } = await import('@/api/agent')
    const p = projectWithDeployment()
    p.pipelines = [{
      id: 'deploy-dev',
      name: 'Deploy Dev',
      services: ['web'],
      pipeline: { deploy: [{ name: 'Deploy', type: 'remote_command' }] },
    }]
    const wrapper = mount(ProjectConfigEditor, { props: { project: p } })
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      pipelines: [expect.objectContaining({ id: 'deploy-dev', name: 'Deploy Dev' })],
    }))
  })

  it('保存服务配置时不被已有项目级 pipeline 校验错误阻塞', async () => {
    const { api } = await import('@/api/agent')
    const p = projectWithDeployment()
    p.pipelines = [{
      id: 'broken',
      name: 'Broken',
      services: ['web'],
      pipeline: { deploy: [{ name: '', type: '' }] },
    }]
    const wrapper = mount(ProjectConfigEditor, { props: { project: p } })
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).not.toContain('项目流水线')
    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      pipelines: [expect.objectContaining({ id: 'broken', name: 'Broken' })],
    }))
  })
})
