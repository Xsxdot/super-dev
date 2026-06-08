/**
 * AgentCreateModal tests the new-agent wizard.
 *
 * 职责：
 *   - 验证新增 Agent 时 TLS 手动字段只在手动模式下出现
 *   - 验证提交 payload 不把非手动 TLS 模式与旧 SNI 值混在一起
 *
 * 边界：
 *   - 不访问真实 agent HTTP API
 *   - 不测试 AgentManagerTab 列表布局
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AgentCreateModal from '../AgentCreateModal.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentCreatePayload, Host } from '@/api/agent'

const hosts: Host[] = [
  {
    id: 'h1',
    name: 'us-01',
    tags: [],
  },
]

async function mountOnSecurityStep() {
  const wrapper = mount(AgentCreateModal, {
    props: { visible: true, hosts },
    global: { plugins: [installTestI18n()] },
  })
  await wrapper.find('[data-test="agent-create-next"]').trigger('click')
  await wrapper.find('[data-test="agent-create-next"]').trigger('click')
  return wrapper
}

describe('AgentCreateModal', () => {
  it('shows SNI and CA certificate fields only for manual TLS', async () => {
    const wrapper = await mountOnSecurityStep()

    expect(wrapper.find('[data-test="agent-create-server-name"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-create-ca-cert"]').exists()).toBe(false)

    await wrapper.find('[data-test="agent-create-tls-off"]').setValue(true)
    expect(wrapper.find('[data-test="agent-create-server-name"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-create-ca-cert"]').exists()).toBe(false)

    await wrapper.find('[data-test="agent-create-tls-manual"]').setValue(true)
    expect(wrapper.find('[data-test="agent-create-server-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-create-ca-cert"]').exists()).toBe(true)
  })

  it('omits stale SNI values after switching manual TLS back to auto', async () => {
    const wrapper = await mountOnSecurityStep()

    await wrapper.find('[data-test="agent-create-tls-manual"]').setValue(true)
    await wrapper.find('[data-test="agent-create-server-name"]').setValue('agent.internal')
    await wrapper.find('[data-test="agent-create-tls-auto"]').setValue(true)
    await wrapper.find('[data-test="agent-create-submit"]').trigger('click')

    const submit = wrapper.emitted('submit')?.[0]?.[0] as AgentCreatePayload | undefined
    expect(submit).toMatchObject({
      security: {
        tls: { mode: 'auto' },
      },
    })
    expect(submit?.security.tls).not.toHaveProperty('server_name')
  })
})
