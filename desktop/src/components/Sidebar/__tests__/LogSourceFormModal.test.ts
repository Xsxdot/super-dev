/**
 * LogSourceFormModal 测试远程监听任务表单。
 *
 * 职责：
 *   - 验证必填项控制提交按钮
 *   - 验证提交 payload 包含名称、类型和 host_ids
 *
 * 边界：
 *   - 不调用 API
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import LogSourceFormModal from '@/components/Sidebar/LogSourceFormModal.vue'
import { useRemoteStore } from '@/stores/remote'

describe('LogSourceFormModal', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('未填名称或主机时提交按钮禁用', () => {
    const wrapper = mount(LogSourceFormModal, { props: { visible: true } })
    const button = wrapper.find('[data-test="logsource-form-submit"]')

    expect(button.attributes('disabled')).toBeDefined()
  })

  it('填好后 submit 携带正确 payload', async () => {
    const store = useRemoteStore()
    store.hosts = [
      {
        id: 'h1',
        name: 'host-01',
        ssh_host: '',
        ssh_port: 22,
        ssh_user: '',
        remote_agent_port: 57017,
        tags: [],
        created_at: '',
        updated_at: '',
      },
    ]
    const wrapper = mount(LogSourceFormModal, { props: { visible: true } })

    await wrapper.find('[data-test="logsource-form-name"]').setValue('nova-api')
    await wrapper.findAll('[data-test="logsource-form-host"] input[type=checkbox]')[0].setValue(true)
    await wrapper.find('[data-test="logsource-form-submit"]').trigger('click')

    const payload = wrapper.emitted('submit')![0][0]
    expect(payload).toMatchObject({
      name: 'nova-api',
      type: 'journalctl',
      host_ids: ['h1'],
    })
  })
})
