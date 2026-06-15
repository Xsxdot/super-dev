import { afterEach, describe, it, expect, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { api, type Deployment, type RuntimeSchema, type ServiceLanguage } from '@/api/agent'
import DeploymentForm from '@/components/Settings/DeploymentForm.vue'
import { installTestI18n } from '@/test-utils/i18n'

function localDep(): Deployment {
  return {
    id: 'd1',
    env_name: 'dev',
    location: 'local',
    control_mode: 'managed',
    runtime: { type: 'command', command: 'go run .', working_dir: '/tmp' },
    logs: { type: 'process' },
    status: '',
  }
}

function systemdRemoteDep(): Deployment {
  return {
    id: 'd1',
    env_name: 'prod',
    location: 'remote',
    control_mode: 'monitor',
    host_ids: ['h1'],
    runtime: { type: 'systemd', service_name: 'api' },
    logs: { type: 'journalctl', target: 'api.service' },
    status: '',
  }
}

function languageDep(): Deployment {
  return {
    id: 'd1',
    env_name: 'dev',
    location: 'local',
    control_mode: 'managed',
    runtime: {
      type: 'language',
      cwd: './server',
      env: { ENABLE_FEATURE: 'true' },
      config: { program: './cmd/api' },
    },
    logs: { type: 'process' },
    status: '',
  }
}

function goRuntimeSchema(): RuntimeSchema {
  return {
    language: 'go',
    version: 1,
    title: {
      key: 'runtime.go.title',
      default: 'Go runtime',
      values: { 'zh-CN': 'Go 运行时' },
    },
    fields: [
      {
        key: 'program',
        name: {
          key: 'runtime.go.program.name',
          default: 'Go entry package',
          values: { 'zh-CN': 'Go 入口包' },
        },
        desc: {
          key: 'runtime.go.program.desc',
          default: 'Main package used by go run.',
          values: { 'zh-CN': '用于 go run 的 main 包' },
        },
        type: 'string',
        required: true,
      },
      {
        key: 'watch',
        name: { key: 'runtime.go.watch.name', default: 'Watch files' },
        desc: { key: 'runtime.go.watch.desc', default: 'Restart when files change.' },
        type: 'boolean',
        required: false,
      },
    ],
  }
}

function nodeRuntimeSchema(): RuntimeSchema {
  return {
    language: 'node',
    version: 1,
    title: { key: 'runtime.node.title', default: 'Node.js' },
    fields: [
      {
        key: 'package_manager',
        name: { key: 'runtime.node.packageManager.name', default: 'Package manager' },
        desc: { key: 'runtime.node.packageManager.desc', default: 'pnpm, npm or yarn; used to run the script.' },
        type: 'string',
        required: false,
        default: 'pnpm',
        group: 'basic',
        order: 10,
      },
      {
        key: 'script',
        name: { key: 'runtime.node.script.name', default: 'Script' },
        desc: { key: 'runtime.node.script.desc', default: 'package.json script to run, e.g. dev.' },
        type: 'string',
        required: false,
        group: 'basic',
        order: 20,
      },
      {
        key: 'program',
        name: { key: 'runtime.node.program.name', default: 'Entry file' },
        desc: { key: 'runtime.node.program.desc', default: 'Run a JS file directly instead of a script, e.g. src/index.js.' },
        type: 'string',
        required: false,
        group: 'advanced',
        order: 30,
      },
      {
        key: 'program_args',
        name: { key: 'runtime.node.programArgs.name', default: 'Program arguments' },
        desc: { key: 'runtime.node.programArgs.desc', default: 'Arguments passed to the application.' },
        type: 'string_array',
        required: false,
        group: 'advanced',
        order: 40,
      },
      {
        key: 'node_args',
        name: { key: 'runtime.node.nodeArgs.name', default: 'Node arguments' },
        desc: { key: 'runtime.node.nodeArgs.desc', default: 'Arguments passed to the node binary (before the entry file).' },
        type: 'string_array',
        required: false,
        group: 'advanced',
        order: 50,
      },
    ],
  }
}

function pythonRuntimeSchema(): RuntimeSchema {
  return {
    language: 'python',
    version: 1,
    title: { key: 'runtime.python.title', default: 'Python' },
    fields: [
      {
        key: 'program',
        name: { key: 'runtime.python.program.name', default: 'Entry file' },
        desc: { key: 'runtime.python.program.desc', default: 'Python entry to start and debug, e.g. main.py. Mutually exclusive with module.' },
        type: 'string',
        required: false,
        group: 'basic',
        order: 10,
      },
      {
        key: 'module',
        name: { key: 'runtime.python.module.name', default: 'Module (-m)' },
        desc: { key: 'runtime.python.module.desc', default: 'Run as python -m <module>. Mutually exclusive with program.' },
        type: 'string',
        required: false,
        group: 'basic',
        order: 20,
      },
      {
        key: 'program_args',
        name: { key: 'runtime.python.programArgs.name', default: 'Program arguments' },
        desc: { key: 'runtime.python.programArgs.desc', default: 'Arguments passed to the application.' },
        type: 'string_array',
        required: false,
        group: 'advanced',
        order: 30,
      },
    ],
  }
}

function runtimeSchemaForLanguage(language: ServiceLanguage): RuntimeSchema {
  if (language === 'node') return nodeRuntimeSchema()
  if (language === 'python') return pythonRuntimeSchema()
  return goRuntimeSchema()
}

function mockLanguageRuntimeSchemas() {
  return vi.spyOn(api, 'describeLanguageRuntimeSchema').mockImplementation(async language => runtimeSchemaForLanguage(language))
}

describe('DeploymentForm', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('local 时展示命令/工作目录输入', () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    expect(wrapper.find('[data-test="dep-command"]').exists()).toBe(true)
  })

  it('切到 remote emit location=remote', async () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    await wrapper.find('[data-test="dep-location-remote"]').setValue()
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.location).toBe('remote')
  })

  it('修改命令 emit runtime.command 新值', async () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    await wrapper.find('[data-test="dep-command"]').setValue('npm run dev')
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.runtime?.command).toBe('npm run dev')
  })

  it('local 时渲染 runtime.env_vars 环境变量编辑器', () => {
    const dep = {
      id: 'd1',
      env_name: 'dev',
      location: 'local' as const,
      runtime: { type: 'command' as const, command: 'x', working_dir: '/tmp', env_vars: { A: '1' } },
      status: '' as const,
    }
    const wrapper = mount(DeploymentForm, { props: { modelValue: dep, hosts: [] } })
    expect(wrapper.find('[data-test="env-row"]').exists()).toBe(true)
  })

  it('remote 勾选主机 emit 含该 host 的 host_ids', async () => {
    const dep = { id: 'd1', env_name: 'dev', location: 'remote' as const, host_ids: [] as string[], status: '' as const }
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: dep, hosts: [{ id: 'h1', name: 'box1' }, { id: 'h2', name: 'box2' }] },
    })
    const checkboxes = wrapper.findAll('.dep-host input')
    expect(checkboxes.length).toBeGreaterThanOrEqual(2)
    await checkboxes[0].setValue(true)
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    const last = emitted![emitted!.length - 1][0] as { host_ids?: string[] }
    expect(last.host_ids).toContain('h1')
  })

  it('remote 已选但未注册的 host_id 会显示并允许移除', async () => {
    const dep = { id: 'd1', env_name: 'dev', location: 'remote' as const, host_ids: ['h1', 'ghost'] as string[], status: '' as const }
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: dep, hosts: [{ id: 'h1', name: 'box1' }] },
    })

    expect(wrapper.text()).toContain('未注册主机：ghost')
    const missing = wrapper.findAll('label.dep-host').find(label => label.text().includes('ghost'))
    expect(missing).toBeTruthy()
    const input = missing!.find('input')
    expect((input.element as HTMLInputElement).checked).toBe(true)

    await input.setValue(false)

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as { host_ids?: string[] }
    expect(last.host_ids).toEqual(['h1'])
  })

  it('用监控/接管启停单选表达控制模式，不再展示只读、外部托管和自定义启停命令', () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: systemdRemoteDep(), hosts: [{ id: 'h1', name: 'box1' }] },
    })

    expect(wrapper.find('[data-test="dep-control-monitor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dep-control-managed"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dep-read-only"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-start-command"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-stop-command"]').exists()).toBe(false)
    const options = wrapper.findAll('[data-test="dep-target-type"] option').map(option => option.attributes('value'))
    expect(options).not.toContain('external')
  })

  it('切换监控模式时 emit control_mode=monitor 并写出兼容 read_only', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    await wrapper.find('[data-test="dep-control-monitor"]').setValue()

    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.control_mode).toBe('monitor')
    expect(last.read_only).toBe(true)
  })

  it('remote 日志类型写入 logs.type', async () => {
    const dep: Deployment = systemdRemoteDep()
    const wrapper = mount(DeploymentForm, { props: { modelValue: dep, hosts: [{ id: 'h1', name: 'box1' }] } })

    await wrapper.find('[data-test="dep-log-type"]').setValue('docker')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.logs?.type).toBe('docker')
  })

  it('remote systemd 默认只展示服务名，不平铺发布路径和旧启停命令', () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: systemdRemoteDep(), hosts: [{ id: 'h1', name: 'box1' }] },
    })

    expect(wrapper.find('[data-test="dep-service-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="dep-release-dir"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-current-dir"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-exec-start"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-start-command"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-stop-command"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dep-log-type"]').exists()).toBe(true)
  })

  it('修改 systemd 服务名时同步默认 journalctl 日志目标', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: systemdRemoteDep(), hosts: [{ id: 'h1', name: 'box1' }] },
    })

    await wrapper.find('[data-test="dep-service-name"]').setValue('worker')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.runtime?.service_name).toBe('worker')
    expect(last.logs).toEqual({ type: 'journalctl', target: 'worker.service' })
  })

  it('接管方式支持 Launchd 服务并默认联动 macOS 日志', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    const options = wrapper.findAll('[data-test="dep-target-type"] option').map(option => option.attributes('value'))
    expect(options).toContain('launchd')

    await wrapper.find('[data-test="dep-target-type"]').setValue('launchd')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.runtime).toEqual({ type: 'launchd', label: '' })
    expect(last.logs).toEqual({ type: 'macos_log', target: '' })
  })

  it('修改 launchd label 时同步默认 macOS 日志目标，并保存 plist 路径', async () => {
    const dep: Deployment = {
      id: 'd1',
      env_name: 'dev',
      location: 'local',
      control_mode: 'managed',
      runtime: { type: 'launchd', label: 'com.example.api' },
      logs: { type: 'macos_log', target: 'com.example.api' },
      status: '',
    }
    const wrapper = mount(DeploymentForm, { props: { modelValue: dep, hosts: [] } })

    await wrapper.find('[data-test="dep-launchd-label"]').setValue('com.example.worker')

    const labelUpdate = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(labelUpdate.runtime?.label).toBe('com.example.worker')
    expect(labelUpdate.logs).toEqual({ type: 'macos_log', target: 'com.example.worker' })

    await wrapper.setProps({ modelValue: labelUpdate })
    await wrapper.find('[data-test="dep-launchd-plist"]').setValue('~/Library/LaunchAgents/com.example.worker.plist')

    const plistUpdate = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(plistUpdate.runtime?.plist_path).toBe('~/Library/LaunchAgents/com.example.worker.plist')
  })

  it('日志来源支持文件 tail', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: systemdRemoteDep(), hosts: [{ id: 'h1', name: 'box1' }] },
    })

    await wrapper.find('[data-test="dep-log-type"]').setValue('file_tail')
    const first = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    await wrapper.setProps({ modelValue: first })
    await wrapper.find('[data-test="dep-log-path"]').setValue('/var/log/api/app.log')

    const last = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(last.logs).toEqual({ type: 'file_tail', path: '/var/log/api/app.log' })
  })

  it('日志来源支持自定义日志命令', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: systemdRemoteDep(), hosts: [{ id: 'h1', name: 'box1' }] },
    })

    await wrapper.find('[data-test="dep-log-type"]').setValue('command')
    const first = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    await wrapper.setProps({ modelValue: first })
    await wrapper.find('[data-test="dep-log-command"]').setValue('tail -F /var/log/api/app.log')

    const last = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(last.logs).toEqual({ type: 'command', command: 'tail -F /var/log/api/app.log' })
  })

  it('local deployment 支持配置 Web Entry 和 AI Debug', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    expect(wrapper.find('[data-test="dep-web-enabled"]').exists()).toBe(true)
    await wrapper.find('[data-test="dep-web-enabled"]').setValue(true)
    const enabled = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(enabled.web?.enabled).toBe(true)

    await wrapper.setProps({ modelValue: enabled })
    await wrapper.find('[data-test="dep-web-url"]').setValue('http://127.0.0.1:3000')
    const withURL = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(withURL.web?.url).toBe('http://127.0.0.1:3000')

    await wrapper.setProps({ modelValue: withURL })
    await wrapper.find('[data-test="dep-web-ai-debug"]').setValue(true)
    const withDebug = wrapper.emitted('update:modelValue')!.at(-1)![0] as Deployment
    expect(withDebug.web?.ai_debug?.enabled).toBe(true)
  })

  it('shows code debug section for local command deployment', () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    expect(wrapper.find('[data-test="code-debug-section"]').exists()).toBe(true)
  })

  it('does not render the dead code_debug program input', () => {
    const wrapper = mount(DeploymentForm, {
      props: {
        modelValue: { ...localDep(), code_debug: { policy: 'auto', program: '.' } },
        hosts: [],
      },
    })

    expect(wrapper.find('[data-test="code-debug-program"]').exists()).toBe(false)
  })

  it('renders language runtime schema fields and writes changes into runtime.config', async () => {
    vi.spyOn(api, 'describeLanguageRuntimeSchema').mockResolvedValue(goRuntimeSchema())

    const wrapper = mount(DeploymentForm, {
      props: { modelValue: languageDep(), hosts: [], serviceLanguage: 'go' },
      global: { plugins: [installTestI18n('zh-CN')] },
    })
    await flushPromises()

    expect(api.describeLanguageRuntimeSchema).toHaveBeenCalledWith('go')
    expect(wrapper.text()).toContain('Go 入口包')
    expect(wrapper.find('[data-test="code-debug-section"]').exists()).toBe(false)

    await wrapper.get('[data-test="schema-field-program"]').setValue('./cmd/worker')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.runtime).toMatchObject({
      type: 'language',
      cwd: './server',
      env: { ENABLE_FEATURE: 'true' },
      config: { program: './cmd/worker' },
    })
  })

  it('renders escape-hatch fields and writes runtime_executable', async () => {
    mockLanguageRuntimeSchemas()

    const wrapper = mount(DeploymentForm, {
      props: {
        modelValue: { ...languageDep(), runtime: { type: 'language', config: {} } },
        hosts: [],
        serviceLanguage: 'node',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await flushPromises()

    const executable = wrapper.find('[data-test="dep-escape-executable"]')
    expect(executable.exists()).toBe(true)

    await executable.setValue('pnpm')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.runtime?.config?.runtime_executable).toBe('pnpm')
  })

  it('shows override notice when escape hatch is set', async () => {
    mockLanguageRuntimeSchemas()

    const wrapper = mount(DeploymentForm, {
      props: {
        modelValue: { ...languageDep(), runtime: { type: 'language', config: { runtime_executable: 'make' } } },
        hosts: [],
        serviceLanguage: 'node',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await flushPromises()

    expect(wrapper.find('[data-test="dep-escape-override-notice"]').exists()).toBe(true)
  })

  it('renders node package_manager and script fields', async () => {
    mockLanguageRuntimeSchemas()

    const wrapper = mount(DeploymentForm, {
      props: {
        modelValue: { ...languageDep(), runtime: { type: 'language', config: {} } },
        hosts: [],
        serviceLanguage: 'node',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await flushPromises()

    expect(wrapper.find('[data-test="schema-field-package_manager"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="schema-field-script"]').exists()).toBe(true)
  })

  it('renders python program field', async () => {
    mockLanguageRuntimeSchemas()

    const wrapper = mount(DeploymentForm, {
      props: {
        modelValue: { ...languageDep(), runtime: { type: 'language', config: {} } },
        hosts: [],
        serviceLanguage: 'python',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })
    await flushPromises()

    expect(wrapper.find('[data-test="schema-field-program"]').exists()).toBe(true)
  })

  it('offers language runtime for managed services with a known language', async () => {
    vi.spyOn(api, 'describeLanguageRuntimeSchema').mockResolvedValue(goRuntimeSchema())

    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [], serviceLanguage: 'go' },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const options = wrapper.findAll('[data-test="dep-target-type"] option').map(option => option.attributes('value'))
    expect(options).toContain('language')

    await wrapper.find('[data-test="dep-target-type"]').setValue('language')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.runtime).toEqual({ type: 'language', cwd: '/tmp', env: {}, config: {} })
    expect(last.logs).toEqual({ type: 'process' })
  })

  it('emits policy=disabled when user disables code debug', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    await wrapper.find('[data-test="code-debug-policy"]').setValue('disabled')

    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.code_debug?.policy).toBe('disabled')
  })

  it('only renders runtime and log controls', () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(false)
  })
})
