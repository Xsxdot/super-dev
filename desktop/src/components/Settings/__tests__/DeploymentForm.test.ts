import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DeploymentForm from '@/components/Settings/DeploymentForm.vue'
import type { Deployment } from '@/api/agent'

function localDep(): Deployment {
  return {
    id: 'd1',
    env_name: 'dev',
    location: 'local',
    runtime: { type: 'command', command: 'go run .', working_dir: '/tmp' },
    logs: { type: 'process' },
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

  it('切换只读开关时 emit read_only 更新', async () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    await wrapper.find('[data-test="dep-read-only"]').setValue(true)

    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toBeTruthy()
    expect((emitted![0]![0] as Deployment).read_only).toBe(true)
  })

  it('remote 日志类型写入 logs.type', async () => {
    const dep: Deployment = {
      id: 'd1',
      env_name: 'dev',
      location: 'remote',
      host_ids: ['h1'],
      runtime: { type: 'systemd', service_name: 'api' },
      logs: { type: 'journalctl', target: 'api.service' },
      status: '',
    }
    const wrapper = mount(DeploymentForm, { props: { modelValue: dep, hosts: [{ id: 'h1', name: 'box1' }] } })

    await wrapper.find('[data-test="dep-log-type"]').setValue('docker')

    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.logs?.type).toBe('docker')
  })

  it('does not render deployment-level pipeline wizard', () => {
    const wrapper = mount(DeploymentForm, {
      props: { modelValue: localDep(), hosts: [] },
    })

    expect(wrapper.find('[data-test="pipeline-enable"]').exists()).toBe(false)
  })
})
