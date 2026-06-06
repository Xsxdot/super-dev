/**
 * HostFormModal 测试单主机身份表单。
 *
 * 职责：
 *   - 验证 Host identity-only 字段渲染
 *   - 验证入口地址元数据随表单提交
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不测试 Agent 连接配置
 */
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import HostFormModal from '@/components/Settings/HostFormModal.vue'
import { installTestI18n } from '@/test-utils/i18n'

describe('HostFormModal', () => {
  it('uses shared settings modal and field classes', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('.settings-modal-backdrop').exists()).toBe(true)
    expect(wrapper.find('.settings-modal').exists()).toBe(true)
    expect(wrapper.findAll('.settings-field').length).toBeGreaterThan(0)
    expect(wrapper.find('[data-test="host-form-name"]').classes()).toContain('settings-input')
  })

  it('renders identity-only host fields', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-public-ip"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-private-ip"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-host"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-form-agent-port"]').exists()).toBe(false)
  })

  it('emits public and private IP fields', async () => {
    const wrapper = mount(HostFormModal, {
      props: { visible: true, initial: null },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    await wrapper.find('[data-test="host-form-name"]').setValue('edge')
    await wrapper.find('[data-test="host-form-public-ip"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="host-form-private-ip"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toEqual(expect.objectContaining({
      name: 'edge',
      public_ip: '203.0.113.10',
      private_ip: '10.0.0.10',
    }))
  })

  it('hydrates existing identity fields when editing', async () => {
    const wrapper = mount(HostFormModal, {
      props: {
        visible: true,
        initial: {
          id: 'host-1',
          name: 'edge',
          public_ip: '203.0.113.10',
          private_ip: '10.0.0.10',
          tags: ['prod'],
        },
      },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    expect((wrapper.find('[data-test="host-form-name"]').element as HTMLInputElement).value).toBe('edge')
    expect((wrapper.find('[data-test="host-form-public-ip"]').element as HTMLInputElement).value).toBe('203.0.113.10')
  })
})
