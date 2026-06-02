import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DeploymentForm from '@/components/Settings/DeploymentForm.vue'
import type { Deployment } from '@/api/agent'

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

describe('DeploymentForm', () => {
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

  it('only renders runtime and log controls', () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(false)
  })
})
