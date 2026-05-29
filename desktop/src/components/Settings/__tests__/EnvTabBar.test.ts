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
})
