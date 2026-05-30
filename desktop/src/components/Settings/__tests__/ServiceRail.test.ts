import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ServiceRail from '@/components/Settings/ServiceRail.vue'
import type { ConfigDraftService } from '@/lib/configDraft'

function svc(id: string, name: string, extra?: Partial<ConfigDraftService>): ConfigDraftService {
  return { id, name, required: false, order: 0, deployments: [], ...extra }
}

describe('ServiceRail', () => {
  it('渲染服务列表', () => {
    const wrapper = mount(ServiceRail, {
      props: { services: [svc('s1', 'web'), svc('s2', 'api')], activeId: 's1', envName: 'dev' },
    })
    expect(wrapper.findAll('[data-test="rail-item"]')).toHaveLength(2)
  })

  it('点击服务 emit select，传递 id', async () => {
    const wrapper = mount(ServiceRail, {
      props: { services: [svc('s1', 'web')], activeId: '', envName: 'dev' },
    })
    await wrapper.find('[data-test="rail-item"]').trigger('click')
    expect(wrapper.emitted('select')![0][0]).toBe('s1')
  })

  it('点击新增 emit add', async () => {
    const wrapper = mount(ServiceRail, {
      props: { services: [], activeId: '', envName: 'dev' },
    })
    await wrapper.find('[data-test="rail-add"]').trigger('click')
    expect(wrapper.emitted('add')).toBeTruthy()
  })

  it('点击删除 emit remove 并传递索引', async () => {
    const wrapper = mount(ServiceRail, {
      props: { services: [svc('s1', 'web'), svc('s2', 'api')], activeId: 's1', envName: 'dev' },
    })
    const delBtns = wrapper.findAll('[data-test="rail-del"]')
    await delBtns[1].trigger('click')
    expect(wrapper.emitted('remove')![0][0]).toBe(1)
  })

  it('未配置时显示「未配置」徽标', () => {
    const wrapper = mount(ServiceRail, {
      props: { services: [svc('s1', 'web')], activeId: 's1', envName: 'dev' },
    })
    expect(wrapper.text()).toContain('未配置')
  })

  it('本地 deployment 显示「本地」徽标', () => {
    const s = svc('s1', 'web', {
      deployments: [{ id: 'd1', env_name: 'dev', location: 'local', command: 'go run .', work_dir: '', status: '' }],
    })
    const wrapper = mount(ServiceRail, {
      props: { services: [s], activeId: 's1', envName: 'dev' },
    })
    expect(wrapper.text()).toContain('本地')
  })

  it('选中项有 active class', () => {
    const wrapper = mount(ServiceRail, {
      props: { services: [svc('s1', 'web'), svc('s2', 'api')], activeId: 's1', envName: 'dev' },
    })
    const items = wrapper.findAll('[data-test="rail-item"]')
    expect(items[0].classes()).toContain('active')
    expect(items[1].classes()).not.toContain('active')
  })
})
