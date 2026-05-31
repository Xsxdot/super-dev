import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DeploymentForm from '@/components/Settings/DeploymentForm.vue'
import type { Deployment } from '@/api/agent'

function localDep(): Deployment {
  return { id: 'd1', env_name: 'dev', location: 'local', command: 'go run .', work_dir: '/tmp', status: '' }
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

  it('修改命令 emit 新值', async () => {
    const wrapper = mount(DeploymentForm, { props: { modelValue: localDep(), hosts: [] } })
    await wrapper.find('[data-test="dep-command"]').setValue('npm run dev')
    const emitted = wrapper.emitted('update:modelValue')
    const last = emitted![emitted!.length - 1][0] as Deployment
    expect(last.command).toBe('npm run dev')
  })

  it('local 时渲染环境变量编辑器', () => {
    const dep = { id: 'd1', env_name: 'dev', location: 'local' as const, command: 'x', work_dir: '/tmp', env: { A: '1' }, status: '' as const }
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
})
