import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectConfigEditor from '@/components/Settings/ProjectConfigEditor.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { Project } from '@/api/agent'
import { dataSourceApi } from '@/api/datasources'

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

vi.mock('@/api/datasources', async () => {
  const actual = await vi.importActual<typeof import('@/api/datasources')>('@/api/datasources')
  return {
    ...actual,
    dataSourceApi: {
      ...actual.dataSourceApi,
      list: vi.fn().mockResolvedValue([]),
      leases: vi.fn().mockResolvedValue([]),
      dryRun: vi.fn().mockResolvedValue({ plans: [], masked_dsns: [], succeeded: true }),
    },
  }
})

function project(): Project {
  return {
    id: 'p1', name: 'demo', root_path: '/tmp/demo', env_selected_service_ids: {},
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    debug_credentials: [],
    services: [{ id: 's1', project_id: 'p1', name: 'web', status: '', required: false, order: 0, deployments: [], debug_credentials: [] }],
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
    expect(wrapper.find('[data-test="project-config-scope"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="project-debug-credentials"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="service-card"]').exists()).toBe(true)
  })

  it('dev 环境默认展示项目级 AI 调试凭据，切到非 dev 后隐藏', async () => {
    const p = project()
    p.environments!.push({ id: 'e2', name: 'prod', is_dev: false, order: 1 })
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="project-debug-credentials"]').exists()).toBe(true)

    await wrapper.findAll('[data-test="env-tab"]')[1].trigger('click')

    expect(wrapper.find('[data-test="project-debug-credentials"]').exists()).toBe(false)
  })

  it('在环境设置中展示并保存 is_dev', async () => {
    const { api } = await import('@/api/agent')
    const p = project()
    p.environments!.push({ id: 'e2', name: 'prod', is_dev: false, order: 1 })
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    await wrapper.findAll('[data-test="env-tab"]')[1].trigger('click')
    const devToggle = wrapper.find('[data-test="env-is-dev"]')
    expect(devToggle.exists()).toBe(true)
    expect((devToggle.element as HTMLInputElement).checked).toBe(false)

    await devToggle.setValue(true)
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      environments: expect.arrayContaining([
        expect.objectContaining({ name: 'prod', is_dev: true }),
      ]),
    }))
  })

  it('通过环境设置改名时同步 deployment 归属', async () => {
    const { api } = await import('@/api/agent')
    const p = projectWithDeployment()
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    const nameInput = wrapper.find('[data-test="env-name-input"]')
    expect(nameInput.exists()).toBe(true)
    await nameInput.setValue('local')
    await nameInput.trigger('blur')
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      environments: [expect.objectContaining({ name: 'local' })],
      services: [expect.objectContaining({
        deployments: [expect.objectContaining({ env_name: 'local' })],
      })],
    }))
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

  it('保存项目级调试凭据输入', async () => {
    const { api } = await import('@/api/agent')
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="project-debug-credentials"] [data-test="debug-credential-add"]').trigger('click')
    const row = wrapper.find('[data-test="project-debug-credentials"] [data-test="debug-credential-row"]')
    await row.find('[data-test="debug-credential-name"]').setValue('test_login')
    await row.find('[data-test="debug-credential-value"]').setValue('demo/demo123')
    await row.find('[data-test="debug-credential-desc"]').setValue('项目级测试登录')
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      debug_credentials: [{
        name: 'test_login',
        value: 'demo/demo123',
        desc: '项目级测试登录',
      }],
    }))
  })

  it('保存项目和环境 AI 指引字段', async () => {
    const { api } = await import('@/api/agent')
    const p = project()
    const wrapper = mountProjectConfigEditor(p)
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="project-ai-note"]').setValue('项目说明')
    await wrapper.find('[data-test="project-auth-hint"]').setValue('先调用 /api/login')
    await wrapper.find('[data-test="env-ai-note"]').setValue('dev 可写 smoke test')
    await wrapper.find('[data-test="env-auth-hint"]').setValue('使用 test_login 换 token')
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      ai_note: '项目说明',
      auth_hint: '先调用 /api/login',
      environments: [expect.objectContaining({
        name: 'dev',
        ai_note: 'dev 可写 smoke test',
        auth_hint: '使用 test_login 换 token',
      })],
    }))
  })

  it('同一项目轮询刷新不会覆盖未保存的本地草稿', async () => {
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="project-debug-credentials"] [data-test="debug-credential-add"]').trigger('click')
    await wrapper
      .find('[data-test="project-debug-credentials"] [data-test="debug-credential-name"]')
      .setValue('draft_login')

    const refreshed = project()
    refreshed.name = 'demo refreshed'
    await wrapper.setProps({ project: refreshed })

    expect(wrapper.get('[data-test="project-debug-credentials"] [data-test="debug-credential-name"]').element)
      .toHaveProperty('value', 'draft_login')
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

  it('渲染数据源区块并按已登记实例填充下拉', async () => {
    vi.mocked(dataSourceApi.list).mockResolvedValueOnce([
      { id: 'pg-1', kind: 'postgres', name: 'local-pg', host: '127.0.0.1', port: 5432, probe: { ok: true } },
      { id: 'redis-1', kind: 'redis', name: 'local-redis', host: '127.0.0.1', port: 6379, probe: { ok: true } },
    ])
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="project-data-source-binding"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-test="project-pg-datasource"] option').map(option => option.text()).join(' ')).toContain('local-pg')
    expect(wrapper.findAll('[data-test="project-redis-datasource"] option').map(option => option.text()).join(' ')).toContain('local-redis')
  })

  it('保存时把绑定写进 data_source_binding 字段', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(dataSourceApi.list).mockResolvedValueOnce([
      { id: 'pg-1', kind: 'postgres', name: 'local-pg', host: '127.0.0.1', port: 5432, probe: { ok: true } },
      { id: 'redis-1', kind: 'redis', name: 'local-redis', host: '127.0.0.1', port: 6379, probe: { ok: true } },
    ])
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="project-pg-datasource"]').setValue('local-pg')
    await wrapper.find('[data-test="project-pg-dev-database"]').setValue('tk_dev')
    await wrapper.find('[data-test="project-redis-datasource"]').setValue('local-redis')
    await wrapper.find('[data-test="project-max-concurrent-leases"]').setValue(4)
    await wrapper.find('[data-test="project-default-ttl-minutes"]').setValue(45)
    await wrapper.find('[data-test="config-save"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(api.putProjectSetup).toHaveBeenCalledWith('p1', expect.objectContaining({
      data_source_binding: {
        postgres: { datasource_name: 'local-pg', dev_database: 'tk_dev', terminate_connections: true },
        redis: { datasource_name: 'local-redis' },
        max_concurrent_leases: 4,
        default_ttl_minutes: 45,
      },
    }))
  })

  it('踢连接开关默认勾选且展示瞬断代价', async () => {
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    const toggle = wrapper.find('[data-test="project-pg-terminate-connections"]')
    expect((toggle.element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.find('[data-test="project-pg-terminate-warning"]').exists()).toBe(true)
  })

  it('点击试跑调用 dry-run 并渲染步骤与脱敏 DSN', async () => {
    vi.mocked(dataSourceApi.dryRun).mockResolvedValueOnce({
      plans: [{ kind: 'postgres', resource_name: 'sdev_eph_demo_aabbcc', steps: ['克隆 tk_dev'] }],
      masked_dsns: ['postgres://role:***@127.0.0.1/db'],
      succeeded: true,
    })
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    await wrapper.find('[data-test="project-data-source-dry-run"]').trigger('click')
    await new Promise(r => setTimeout(r))

    expect(dataSourceApi.dryRun).toHaveBeenCalledWith('p1')
    expect(wrapper.find('[data-test="project-dry-run-result"]').text()).toContain('postgres://role:***@127.0.0.1/db')
    expect(wrapper.find('[data-test="project-dry-run-result"]').text()).toContain('克隆 tk_dev')
  })

  it('未登记数据源时给出去设置页登记的引导', async () => {
    vi.mocked(dataSourceApi.list).mockResolvedValueOnce([])
    const wrapper = mountProjectConfigEditor(project())
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="project-data-source-register-hint"]').exists()).toBe(true)
  })
})
