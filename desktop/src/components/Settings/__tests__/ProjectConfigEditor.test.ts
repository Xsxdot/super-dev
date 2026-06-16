import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import { installTestI18n } from '@/test-utils/i18n'
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

function mountProjectConfigEditor(projectValue: Project, locale: 'zh-CN' | 'en-US' = 'zh-CN') {
  return mount(ProjectConfigEditor, {
    props: { project: projectValue },
    global: { plugins: [installTestI18n(locale)] },
  })
}

describe('ProjectConfigEditor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('uses shared settings wide modal shell', async () => {
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('.settings-modal-backdrop').exists()).toBe(true)
    expect(wrapper.find('.settings-modal-wide').exists()).toBe(true)
    expect(wrapper.find('[data-test="config-save"]').classes()).toContain('settings-btn-primary')
    expect(wrapper.find('[data-test="config-cancel"]').classes()).toContain('settings-btn')
  })

  it('渲染 env tab 与服务列表', async () => {
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))
    expect(wrapper.find('[data-test="env-tab"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="service-card"]').exists()).toBe(true)
  })

  it('校验失败时阻止保存并展示错误', async () => {
    const { api } = await import('@/api/agent')
    const p = project()
    p.environments![0].name = ''
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-save"]').trigger('click')
    expect(api.putProjectSetup).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('环境名称不能为空')
  })

  it('点击取消 emit cancel', async () => {
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))
    await wrapper.find('[data-test="config-cancel"]').trigger('click')
    expect(wrapper.emitted('cancel')).toBeTruthy()
  })

  it('校验通过时保存：调用 putProjectSetup 并 emit saved', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mountProjectConfigEditor(project())
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
    const p = project()
    p.services[0].language = 'node'
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="enable-dep"]').trigger('click')
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      services: expect.arrayContaining([
        expect.objectContaining({
          deployments: expect.arrayContaining([
            expect.objectContaining({
              control_mode: 'managed',
              runtime: expect.objectContaining({
                type: 'language',
                cwd: '/tmp/demo/web',
                config: expect.objectContaining({ package_manager: 'pnpm', script: 'dev' }),
              }),
              logs: expect.objectContaining({ type: 'process' }),
            }),
          ]),
        }),
      ]),
    }))
  })

  it('不再渲染项目级 pipeline 编辑区', async () => {
    const wrapper = mountProjectConfigEditor(projectWithDeployment())
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
    const wrapper = mountProjectConfigEditor(p)
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
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).not.toContain('项目流水线')
    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      pipelines: [expect.objectContaining({ id: 'broken', name: 'Broken' })],
    }))
  })

  it('远程主机候选不包含本机节点', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.listHosts).mockResolvedValueOnce([
      { id: 'self-node', name: 'Local Machine', tags: [], is_self: true },
      { id: 'h1', name: 'prod-box', private_ip: '10.0.0.1', tags: [] },
    ])
    const p = project()
    p.services[0].deployments = [{
      id: 'd1',
      env_name: 'dev',
      location: 'remote',
      host_ids: [],
      runtime: { type: 'systemd', service_name: 'web' },
      logs: { type: 'journalctl', target: 'web.service' },
      status: '',
    }]

    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).toContain('prod-box')
    expect(wrapper.text()).not.toContain('Local Machine')
  })

  it('英文 locale 下渲染保存和取消按钮', async () => {
    const wrapper = mountProjectConfigEditor(project(), 'en-US')
    await new Promise(r => setTimeout(r))

    expect(wrapper.text()).toContain('Save')
    expect(wrapper.text()).toContain('Cancel')
  })
})
