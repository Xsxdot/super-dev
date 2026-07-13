import { describe, it, expect } from 'vitest'
import { projectToDraft, draftToPayload, validateDraft, validateDraftDetailed } from '@/lib/configDraft'
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

  it('保留 command runtime 的结构化 executable/args', () => {
    const project = makeProject()
    project.services[0].deployments![0].runtime = {
      type: 'command',
      command: '"C:\\Program Files\\SuperDev\\sample.exe" --port 18191',
      executable: 'C:\\Program Files\\SuperDev\\sample.exe',
      args: ['--port', '18191'],
    }

    const payload = draftToPayload(projectToDraft(project))

    expect(payload.services[0].deployments[0].runtime).toMatchObject({
      type: 'command',
      command: '"C:\\Program Files\\SuperDev\\sample.exe" --port 18191',
      executable: 'C:\\Program Files\\SuperDev\\sample.exe',
      args: ['--port', '18191'],
    })
  })

  it('projectToDraft 将旧只读和 external 归一成 monitor 控制模式', () => {
    const p = makeProject()
    p.services[0].deployments![0] = {
      id: 'd1',
      env_name: 'prod',
      location: 'remote',
      host_ids: ['h1'],
      read_only: true,
      runtime: { type: 'external' },
      logs: { type: 'journalctl', target: 'api.service' },
      status: '',
    }

    const dep = projectToDraft(p).services[0].deployments[0]

    expect(dep.control_mode).toBe('monitor')
    expect(dep.runtime?.type).toBe('systemd')
    expect(dep.runtime?.service_name).toBe('api')
  })

  it('draftToPayload 保存 control_mode 和自定义日志命令', () => {
    const draft = projectToDraft(makeProject())
    const dep = draft.services[0].deployments[0]
    dep.control_mode = 'monitor'
    dep.runtime = { type: 'systemd', service_name: 'api' }
    dep.logs = { type: 'command', command: 'tail -F /var/log/api/app.log' }

    const out = draftToPayload(draft).services[0].deployments[0]

    expect(out.control_mode).toBe('monitor')
    expect(out.read_only).toBe(true)
    expect(out.logs).toEqual({ type: 'command', command: 'tail -F /var/log/api/app.log' })
  })

  it('preserves deployment web entry config through draft payload conversion', () => {
    const project = makeProject()
    project.services[0].deployments![0].web = {
      enabled: true,
      url: 'http://127.0.0.1:3000',
      default_path: '/',
      ai_debug: { enabled: true },
    }

    const payload = draftToPayload(projectToDraft(project))

    expect(payload.services[0].deployments[0].web).toEqual({
      enabled: true,
      url: 'http://127.0.0.1:3000',
      default_path: '/',
      ai_debug: { enabled: true },
    })
  })

  it('preserves deployment autostart fields through draft payload conversion', () => {
    const project = makeProject()
    project.services[0].deployments![0] = {
      id: 'dep-worker-dev',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: { type: 'command', command: 'npm run worker' },
      logs: { type: 'process' },
      status: '',
      start_on_boot: true,
      depends_on: ['svc-server'],
      readiness: { type: 'http', target: 'http://127.0.0.1:9100/', timeout_seconds: 20 },
    }

    const draft = projectToDraft(project)
    const payload = draftToPayload(draft)

    expect(draft.services[0].deployments[0].start_on_boot).toBe(true)
    expect(draft.services[0].deployments[0].depends_on).toEqual(['svc-server'])
    expect(draft.services[0].deployments[0].readiness).toEqual({
      type: 'http',
      target: 'http://127.0.0.1:9100/',
      timeout_seconds: 20,
    })
    expect(payload.services[0].deployments[0].start_on_boot).toBe(true)
    expect(payload.services[0].deployments[0].depends_on).toEqual(['svc-server'])
    expect(payload.services[0].deployments[0].readiness).toEqual({
      type: 'http',
      target: 'http://127.0.0.1:9100/',
      timeout_seconds: 20,
    })
  })

  it('preserves deployment code debug config in setup payload', () => {
    const project = makeProject()
    project.services[0].deployments = [{
      id: 'dep-api-dev',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: { type: 'command', command: 'go run ./cmd/server', working_dir: '/workspace/ai-hub/server' },
      logs: { type: 'process' },
      status: '',
      code_debug: {
        policy: 'enabled',
        mode: 'launch',
        program: 'cmd/server',
        args: ['--dev'],
        working_dir: 'server',
        env_vars: { DEBUG: '1' },
        stop_on_entry: false,
      } as any,
    }]

    const draft = projectToDraft(project)
    const payload = draftToPayload(draft)

    expect(payload.services[0].deployments[0].code_debug).toEqual({
      policy: 'enabled',
      mode: 'launch',
      stop_on_entry: false,
    })
  })

  it('preserves project and service debug credentials through draft payload conversion', () => {
    const project = makeProject()
    project.debug_credentials = [
      { name: 'test_login', value: 'demo/demo123', desc: '项目级测试登录账号' },
    ]
    project.services[0].debug_credentials = [
      { name: 'api_key', value: 'svc-key', desc: '服务 API key' },
    ]

    const draft = projectToDraft(project)
    const payload = draftToPayload(draft)

    expect(draft.debug_credentials).toEqual([
      { name: 'test_login', value: 'demo/demo123', desc: '项目级测试登录账号' },
    ])
    expect(draft.services[0].debug_credentials).toEqual([
      { name: 'api_key', value: 'svc-key', desc: '服务 API key' },
    ])
    expect(payload.debug_credentials).toEqual([
      { name: 'test_login', value: 'demo/demo123', desc: '项目级测试登录账号' },
    ])
    expect(payload.services[0].debug_credentials).toEqual([
      { name: 'api_key', value: 'svc-key', desc: '服务 API key' },
    ])
  })

  it('omits debug credentials with empty names from setup payload', () => {
    const draft = projectToDraft(makeProject())
    draft.debug_credentials = [
      { name: '', value: 'ignored', desc: '未命名项目凭据' },
      { name: 'test_login', value: 'demo/demo123', desc: '测试登录' },
    ]
    draft.services[0].debug_credentials = [
      { name: '   ', value: 'ignored', desc: '未命名服务凭据' },
      { name: 'api_key', value: 'svc-key', desc: '服务 API key' },
    ]

    const payload = draftToPayload(draft)

    expect(payload.debug_credentials).toEqual([
      { name: 'test_login', value: 'demo/demo123', desc: '测试登录' },
    ])
    expect(payload.services[0].debug_credentials).toEqual([
      { name: 'api_key', value: 'svc-key', desc: '服务 API key' },
    ])
  })

  it('preserves AI guidance hints through draft payload conversion', () => {
    const project = makeProject()
    project.ai_note = 'project note'
    project.auth_hint = 'project auth'
    project.environments![0].ai_note = 'env note'
    project.environments![0].auth_hint = 'env auth'
    project.services[0].ai_note = 'service note'
    project.services[0].auth_hint = 'service auth'

    const draft = projectToDraft(project)
    const payload = draftToPayload(draft)

    expect(draft.ai_note).toBe('project note')
    expect(draft.auth_hint).toBe('project auth')
    expect(draft.environments[0]).toMatchObject({ ai_note: 'env note', auth_hint: 'env auth' })
    expect(draft.services[0]).toMatchObject({ ai_note: 'service note', auth_hint: 'service auth' })
    expect(payload).toMatchObject({ ai_note: 'project note', auth_hint: 'project auth' })
    expect(payload.environments[0]).toMatchObject({ ai_note: 'env note', auth_hint: 'env auth' })
    expect(payload.services[0]).toMatchObject({ ai_note: 'service note', auth_hint: 'service auth' })
  })

  it('reports code debug for unsupported deployment runtime', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments = [{
      id: 'dep-api-prod',
      env_name: 'prod',
      location: 'remote',
      control_mode: 'managed',
      runtime: { type: 'systemd', service_name: 'api' },
      logs: { type: 'journalctl', target: 'api.service' },
      host_ids: ['host-1'],
      status: '',
      code_debug: { policy: 'enabled' },
    }]

    const issues = validateDraftDetailed(draft)

    expect(issues).toContainEqual(expect.objectContaining({
      scope: 'config',
      key: 'validation.codeDebugLocalCommandOnly',
    }))
  })

  it('flags invalid code_debug policy', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].code_debug = { policy: 'bogus' as any }

    const errors = validateDraftDetailed(draft)

    expect(errors.some(e => e.key === 'validation.codeDebugPolicyInvalid')).toBe(true)
  })

  it('projectToDraft 和 draftToPayload 保留 launchd runtime 与 macOS 日志', () => {
    const p = makeProject()
    p.services[0].name = 'api'
    p.services[0].deployments![0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: {
        type: 'launchd',
        label: 'com.example.api',
        plist_path: '~/Library/LaunchAgents/com.example.api.plist',
      },
      logs: { type: 'macos_log', target: 'com.example.api' },
      status: '',
    }

    const draft = projectToDraft(p)
    const dep = draft.services[0].deployments[0]
    expect(dep.runtime).toEqual({
      type: 'launchd',
      label: 'com.example.api',
      plist_path: '~/Library/LaunchAgents/com.example.api.plist',
    })
    expect(dep.logs).toEqual({ type: 'macos_log', target: 'com.example.api' })

    const out = draftToPayload(draft).services[0].deployments[0]
    expect(out.runtime).toEqual(dep.runtime)
    expect(out.logs).toEqual(dep.logs)
    expect(out.log_type).toBeUndefined()
  })

  it('projectToDraft 和 draftToPayload 保留 language runtime 的 cwd/env/config', () => {
    const p = makeProject()
    p.services[0].language = 'go'
    p.services[0].deployments![0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: {
        type: 'language',
        cwd: './server',
        env: { ENABLE_FEATURE: 'true', '': 'ignored' },
        config: { program: './cmd/api', watch: true },
      },
      logs: { type: 'process' },
      status: '',
    }

    const draft = projectToDraft(p)
    const dep = draft.services[0].deployments[0]
    expect(dep.runtime).toEqual({
      type: 'language',
      cwd: './server',
      env: { ENABLE_FEATURE: 'true' },
      config: { program: './cmd/api', watch: true },
    })

    const out = draftToPayload(draft).services[0].deployments[0]
    expect(out.runtime).toEqual(dep.runtime)
    expect(out.logs).toEqual({ type: 'process' })
    expect(out.command).toBeUndefined()
    expect(out.work_dir).toBeUndefined()
  })

  it('projectToDraft 为 launchd 默认生成 macOS 日志目标', () => {
    const p = makeProject()
    p.services[0].deployments![0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: { type: 'launchd', label: 'com.example.api' },
      status: '',
    }

    const dep = projectToDraft(p).services[0].deployments[0]
    expect(dep.logs).toEqual({ type: 'macos_log', target: 'com.example.api' })
  })

  it('validateDraft：launchd 接管必须填写 Label，macOS 日志必须填写目标', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].name = 'api'
    draft.services[0].deployments[0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: { type: 'launchd', label: ' ' },
      logs: { type: 'macos_log', target: '' },
      status: '',
    }

    expect(validateDraft(draft)).toContain('服务「api」在「dev」环境的 Launchd Label 不能为空')
    expect(validateDraft(draft)).toContain('服务「api」在「dev」环境的 macOS 日志目标不能为空')
  })

  it('validateDraft：文件 tail 必须填写路径，自定义日志命令必须填写命令', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0].logs = { type: 'file_tail', path: '' }
    expect(validateDraft(draft).some(e => e.includes('日志文件路径'))).toBe(true)

    draft.services[0].deployments[0].logs = { type: 'command', command: '' }
    expect(validateDraft(draft).some(e => e.includes('日志命令'))).toBe(true)
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

  it('validateDraft：同一 service 下 deployment env_name 重复报错', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments.push({
      ...draft.services[0].deployments[0],
      id: 'd2',
      command: 'go run ./alt',
      runtime: { type: 'command', command: 'go run ./alt' },
    })

    expect(validateDraft(draft).some(e => e.includes('同一环境只能配置一个部署'))).toBe(true)
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

  it('projectToDraft 深拷贝嵌套对象：改草稿的 env / logs 不影响原 Project', () => {
    const p = makeProject()
    const origDep = p.services[0]!.deployments![0]!
    origDep.logs = { type: 'command', command: 'tail -F app.log', extra_args: ['--since', '1h'] }
    const draft = projectToDraft(p)
    // 改草稿里的 env map 和 logs 参数
    const draftDep = draft.services[0]!.deployments[0]!
    draftDep.env!.A = 'mutated'
    draftDep.logs!.extra_args![1] = '2h'
    // 原 Project 不应被影响
    expect(origDep.env!.A).toBe('1')
    expect(origDep.logs!.extra_args![1]).toBe('1h')
  })

  it('draftToPayload 透传 extra_args 与 env_file（编辑器未暴露但不应丢失）', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0]!.deployments[0]!.extra_args = ['--since', '1h']
    draft.services[0]!.deployments[0]!.env_file = '.env.local'
    const payload = draftToPayload(draft)
    expect(payload.services[0]!.deployments[0]!.extra_args).toEqual(['--since', '1h'])
    expect(payload.services[0]!.deployments[0]!.env_file).toBe('.env.local')
  })

  it('draftToPayload 用 monitor 控制模式写出兼容 read_only', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0]!.deployments[0]!.control_mode = 'monitor'
    const payload = draftToPayload(draft)
    expect(payload.services[0]!.deployments[0]!.read_only).toBe(true)
    expect(payload.services[0]!.deployments[0]!.control_mode).toBe('monitor')
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

  it('不允许 local command deployment 省略 command', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].deployments[0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      status: '',
      runtime: { type: 'command', command: '' },
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

  it('remote_cmd 同步方式要求 transfer 步骤填写远端同步命令', () => {
    const draft = projectToDraft(makeProject())
    draft.pipelines = [{
      id: 'deploy-prod',
      name: 'Deploy Prod',
      sync_mode: 'remote_cmd',
      pipeline: {
        deploy: [{
          name: 'Upload',
          type: 'transfer',
          with: { source: '${artifact}', target: '/tmp/app.tar.gz' },
        }],
      },
    }]

    expect(validateDraft(draft)).toContain('项目流水线「Deploy Prod」步骤「Upload」的目标机同步命令不能为空')
  })

  it('remote deployment 必须选择主机', () => {
    const draft = projectToDraft(makeProject())
    draft.services[0].name = 'api'
    draft.services[0].deployments[0] = {
      id: 'd1',
      env_name: 'dev',
      location: 'remote',
      host_ids: [],
      status: '',
    }

    expect(validateDraft(draft)).toContain('服务「api」在「dev」环境未选择主机')
  })
})
