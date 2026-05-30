import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EnvTabBar from '@/components/Settings/EnvTabBar.vue'
import type { Environment } from '@/api/agent'

const envs: Environment[] = [
  { id: 'e1', name: 'dev', is_dev: true, order: 0 },
  { id: 'e2', name: 'prod', is_dev: false, order: 1 },
]

describe('EnvTabBar', () => {
  it('渲染每个 env 的 tab', () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    expect(wrapper.findAll('[data-test="env-tab"]')).toHaveLength(2)
  })

  it('点击 tab emit update:active', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    await wrapper.findAll('[data-test="env-tab"]')[1].trigger('click')
    expect(wrapper.emitted('update:active')![0][0]).toBe('prod')
  })

  it('点击新增 emit add-env', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    await wrapper.find('[data-test="add-env"]').trigger('click')
    expect(wrapper.emitted('add-env')).toBeTruthy()
  })

  it('renamingEnv 匹配时渲染改名 input 而非文本', () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev', renamingEnv: 'dev' } })
    expect(wrapper.find('[data-test="env-rename-input"]').exists()).toBe(true)
  })

  it('改名 input 回车提交 emit rename-env', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev', renamingEnv: 'dev' } })
    const input = wrapper.find('[data-test="env-rename-input"]')
    await input.setValue('staging')
    await input.trigger('keydown.enter')
    expect(wrapper.emitted('rename-env')![0]).toEqual(['dev', 'staging'])
  })

  it('改名 input Esc 取消 emit start-rename 空字符串', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev', renamingEnv: 'dev' } })
    const input = wrapper.find('[data-test="env-rename-input"]')
    await input.trigger('keydown.esc')
    expect(wrapper.emitted('start-rename')![0]).toEqual([''])
  })

  it('点击 ✎ 按钮 emit start-rename 该环境名', async () => {
    const wrapper = mount(EnvTabBar, { props: { environments: envs, active: 'dev' } })
    await wrapper.find('[data-test="env-edit"]').trigger('click')
    expect(wrapper.emitted('start-rename')![0]).toEqual(['dev'])
  })
})
