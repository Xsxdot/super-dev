import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ServiceList from '@/components/Settings/ServiceList.vue'
import type { ConfigDraftService } from '@/lib/configDraft'

function svc(): ConfigDraftService {
  return { id: 's1', name: 'web', required: false, order: 0, deployments: [] }
}

describe('ServiceList', () => {
  it('渲染服务卡片', () => {
    const wrapper = mount(ServiceList, { props: { services: [svc()], envName: 'dev', hosts: [] } })
    expect(wrapper.findAll('[data-test="service-card"]')).toHaveLength(1)
  })

  it('点击新增服务 emit add-service', async () => {
    const wrapper = mount(ServiceList, { props: { services: [], envName: 'dev', hosts: [] } })
    await wrapper.find('[data-test="add-service"]').trigger('click')
    expect(wrapper.emitted('add-service')).toBeTruthy()
  })

  it('当前 env 无 deployment 时展示启用占位', () => {
    const wrapper = mount(ServiceList, { props: { services: [svc()], envName: 'dev', hosts: [] } })
    expect(wrapper.find('[data-test="enable-dep"]').exists()).toBe(true)
  })
})
