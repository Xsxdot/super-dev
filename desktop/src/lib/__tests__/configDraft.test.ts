import { describe, it, expect } from 'vitest'
import { projectToDraft, draftToPayload, validateDraft } from '@/lib/configDraft'
import type { Project } from '@/api/agent'

function makeProject(): Project {
  return {
    id: 'p1',
    name: 'demo',
    root_path: '/tmp/demo',
    services: [
      {
        id: 's1', project_id: 'p1', name: 'web', status: '',
        required: false, order: 0,
        deployments: [
          { id: 'd1', env_name: 'dev', location: 'local', command: 'go run .', work_dir: '/tmp/demo', env: { A: '1' }, status: '' },
        ],
      },
    ],
    env_selected_service_ids: {},
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
  }
}

describe('configDraft', () => {
  it('projectToDraft 深拷贝，修改草稿不影响原对象', () => {
    const p = makeProject()
    const draft = projectToDraft(p)
    draft.services[0].name = 'changed'
    expect(p.services[0].name).toBe('web')
  })

  it('draftToPayload 拍平为 SetupPayload，忽略空 key 的 env 变量', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].env = { A: '1', '': 'ignored' }
    const payload = draftToPayload(draft)
    expect(payload.environments).toHaveLength(1)
    expect(payload.services[0].name).toBe('web')
    expect(payload.services[0].deployments[0].env).toEqual({ A: '1' })
  })

  it('projectToDraft 将旧 deployment 字段归一到 runtime/logs', () => {
    const draft = projectToDraft(makeProject())
    const dep = draft.services[0].deployments[0]

    expect(dep.runtime).toEqual({
      type: 'command',
      command: 'go run .',
      working_dir: '/tmp/demo',
      env_vars: { A: '1' },
    })
    expect(dep.logs).toEqual({ type: 'process' })
  })

  it('draftToPayload 保存 runtime/logs 作为服务运行配置', () => {
    const draft = projectToDraft(makeProject())
    const dep = draft.services[0].deployments[0]
    dep.runtime = {
      type: 'command',
      command: 'npm run dev',
      working_dir: '/tmp/demo/web',
      env_file: '.env.dev',
      env_vars: { B: '2', '': 'ignored' },
    }
    dep.logs = { type: 'process' }

    const payload = draftToPayload(draft)
    const out = payload.services[0].deployments[0]
    expect(out.runtime).toEqual({
      type: 'command',
      command: 'npm run dev',
      working_dir: '/tmp/demo/web',
      env_file: '.env.dev',
      env_vars: { B: '2' },
    })
    expect(out.logs).toEqual({ type: 'process' })
    expect(out.command).toBe('npm run dev')
    expect(out.work_dir).toBe('/tmp/demo/web')
    expect(out.env).toEqual({ B: '2' })
  })

  it('validateDraft：env 名称为空报错', () => {
    const draft = projectToDraft(makeProject())
    draft.environments[0].name = ''
    expect(validateDraft(draft)).toContain('环境名称不能为空')
  })

  it('validateDraft：service 名称重复报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services.push({ ...draft.services[0], id: 's2' })
    expect(validateDraft(draft).some(e => e.includes('服务名'))).toBe(true)
  })

  it('validateDraft：local deployment 命令为空报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].runtime!.command = ''
    expect(validateDraft(draft).some(e => e.includes('命令'))).toBe(true)
  })

  it('validateDraft：remote deployment 未选 host 报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0] = {
      id: 'd1', env_name: 'dev', location: 'remote', host_ids: [], status: '',
    } as never
    expect(validateDraft(draft).some(e => e.includes('主机'))).toBe(true)
  })

  it('validateDraft：合法草稿返回空数组', () => {
    expect(validateDraft(projectToDraft(makeProject()))).toEqual([])
  })

  it('projectToDraft 深拷贝嵌套对象：改草稿的 env / pipeline 不影响原 Project', () => {
    const p = makeProject()
    const origDep = p.services[0]!.deployments![0]!
    origDep.pipeline = { build: [{ name: 'Build', type: 'local_command', with: { cmd: 'make' } }] }
    const draft = projectToDraft(p)
    // 改草稿里的 env map 和 pipeline 步骤
    const draftDep = draft.services[0]!.deployments[0]!
    draftDep.env!.A = 'mutated'
    ;(draftDep.pipeline!.build![0]!.with as Record<string, unknown>).cmd = 'mutated'
    // 原 Project 不应被影响
    expect(origDep.env!.A).toBe('1')
    expect(origDep.pipeline!.build![0]!.with!.cmd).toBe('make')
  })

  it('draftToPayload 透传 extra_args 与 env_file（编辑器未暴露但不应丢失）', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0]!.deployments[0]!.extra_args = ['--since', '1h']
    draft.services[0]!.deployments[0]!.env_file = '.env.local'
    const payload = draftToPayload(draft)
    expect(payload.services[0]!.deployments[0]!.extra_args).toEqual(['--since', '1h'])
    expect(payload.services[0]!.deployments[0]!.env_file).toBe('.env.local')
  })

  it('draftToPayload 透传 read_only', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0]!.deployments[0]!.read_only = true
    const payload = draftToPayload(draft)
    expect(payload.services[0]!.deployments[0]!.read_only).toBe(true)
  })

  it('projectToDraft 和 draftToPayload 保留项目变量、项目流水线、runtime/logs', () => {
    const p: Project = {
      id: 'p1',
      name: 'demo',
      root_path: '/tmp/demo',
      variables: { app_name: 'demo' },
      environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
      services: [{
        id: 'svc-api',
        project_id: 'p1',
        name: 'api',
        status: '',
        required: true,
        order: 0,
        deployments: [{
          id: 'dep-api-dev',
          env_name: 'dev',
          location: 'local',
          runtime: { type: 'command', command: 'go run .' },
          logs: { type: 'process' },
          status: '',
        }],
      }],
      pipelines: [{
        id: 'deploy-dev',
        name: 'Deploy Dev',
        services: ['api'],
        roles: { api_targets: { from_service: 'api' } },
        pipeline: { build: [{ name: 'Build', type: 'local_command' }] },
      }],
    }

    const draft = projectToDraft(p)
    const payload = draftToPayload(draft)
    expect(payload.variables).toEqual({ app_name: 'demo' })
    expect(payload.pipelines?.[0].id).toBe('deploy-dev')
    expect(payload.services[0].deployments[0].runtime?.type).toBe('command')
    expect(payload.services[0].deployments[0].logs?.type).toBe('process')
  })

  it('不允许 local deployment 使用 deployment-level pipeline 替代 command', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      status: '',
      pipeline: { build: [{ name: 'Build', type: 'local_command', with: { cmd: 'go build' } }] },
    }
    expect(validateDraft(draft).some(e => e.includes('命令'))).toBe(true)
  })

  it('校验项目级 pipeline step name 和 type', () => {
    const draft = projectToDraft(makeProject())
    draft.pipelines = [{
      id: 'deploy-dev',
      name: 'Deploy Dev',
      pipeline: { deploy: [{ name: '', type: '' }] },
    }]
    const errors = validateDraft(draft)
    expect(errors.some(e => e.includes('步骤名称不能为空'))).toBe(true)
    expect(errors.some(e => e.includes('插件类型不能为空'))).toBe(true)
  })

  it('remote deployment 必须选择主机，不能依赖 deployment-level pipeline 目标', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].name = 'api'
    draft.services[0].deployments[0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'remote',
      host_ids: [],
      status: '',
      pipeline: {
        roles: { deploy_1_targets: [] },
        deploy: [{ name: 'Deploy', type: 'include', with: { vars: { role: 'deploy_1_targets' } } }],
      },
    }

    expect(validateDraft(draft)).toContain('服务「api」在「dev」环境未选择主机')
  })
})
