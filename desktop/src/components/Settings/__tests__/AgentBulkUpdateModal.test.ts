/**
 * AgentBulkUpdateModal tests the batch update dialog.
 *
 * 职责：
 *   - 验证批量更新弹窗展示目标版本和候选 Agent
 *   - 验证全选/取消全选规则
 *   - 验证更新失败不阻塞其它 Agent
 *
 * 边界：
 *   - 不访问真实后端
 *   - 不测试 AgentManagerTab 工具栏接入
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentBulkUpdateModal from '@/components/Settings/AgentBulkUpdateModal.vue'
import { useAgentsStore } from '@/stores/agents'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentDTO, Host } from '@/api/agent'

function agent(hostId: string, version: string | undefined, health: AgentDTO['runtime']['health'] = 'healthy'): AgentDTO {
  return {
    host_id: hostId,
    host_name: hostId,
    tags: [],
    transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }] },
    config: { listen_port: 57017 },
    runtime: { installed: true, health, reachable: health === 'healthy', version },
    security: { token_configured: true, provision_state: 'provisioned', tls: { mode: 'auto' } },
  }
}

function host(id: string, overrides: Partial<Host> = {}): Host {
  return {
    id,
    name: id,
    tags: [],
    ssh_host: '10.0.0.8',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_private_key: 'KEY',
    ...overrides,
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('AgentBulkUpdateModal', () => {
  it('renders selectable and disabled rows from update target metadata', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'getAgentUpdateTarget').mockResolvedValue({ version: '0.2.0', source: 'bundled', concurrency_default: 3 })
    const wrapper = mount(AgentBulkUpdateModal, {
      props: {
        visible: true,
        agents: [agent('h1', '0.1.0'), agent('h2', '0.2.0'), agent('h3', '0.1.0', 'unreachable')],
        hosts: [host('h1'), host('h2'), host('h3')],
      },
      global: { plugins: [installTestI18n()] },
    })
    await vi.waitFor(() => expect(store.getAgentUpdateTarget).toHaveBeenCalled())

    expect(wrapper.text()).toContain('0.2.0')
    expect(wrapper.find('[data-test="bulk-update-row-h1"]').text()).toContain('可更新')
    expect(wrapper.find('[data-test="bulk-update-row-h2"]').text()).toContain('已是最新')
    expect(wrapper.find('[data-test="bulk-update-row-h3"]').text()).toContain('不可达，可尝试')
    expect((wrapper.find('[data-test="bulk-update-checkbox-h1"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.find('[data-test="bulk-update-checkbox-h3"]').element as HTMLInputElement).checked).toBe(false)
  })

  it('selects only default-updateable rows and clears selection', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'getAgentUpdateTarget').mockResolvedValue({ version: '0.2.0', source: 'bundled', concurrency_default: 3 })
    const wrapper = mount(AgentBulkUpdateModal, {
      props: {
        visible: true,
        agents: [agent('h1', '0.1.0'), agent('h2', '0.1.0', 'unreachable')],
        hosts: [host('h1'), host('h2')],
      },
      global: { plugins: [installTestI18n()] },
    })
    await vi.waitFor(() => expect(store.getAgentUpdateTarget).toHaveBeenCalled())

    await wrapper.find('[data-test="bulk-update-clear"]').trigger('click')
    expect((wrapper.find('[data-test="bulk-update-checkbox-h1"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.find('[data-test="bulk-update-select-default"]').trigger('click')
    expect((wrapper.find('[data-test="bulk-update-checkbox-h1"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.find('[data-test="bulk-update-checkbox-h2"]').element as HTMLInputElement).checked).toBe(false)
  })

  it('runs selected updates and keeps later rows after a failure', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'getAgentUpdateTarget').mockResolvedValue({ version: '0.2.0', source: 'bundled', concurrency_default: 3 })
    vi.spyOn(store, 'updateAgentBinary').mockImplementation(async (hostId: string) => {
      if (hostId === 'h2') throw new Error('upload failed')
      return { ok: true, host_id: hostId, platform: 'linux/amd64', version: '0.2.0', message: 'updated', updated_at: '2026-06-08T12:00:00Z' }
    })
    vi.spyOn(store, 'checkAgent').mockResolvedValue(agent('h1', '0.2.0'))
    vi.spyOn(store, 'loadAgents').mockResolvedValue(undefined)
    const wrapper = mount(AgentBulkUpdateModal, {
      props: {
        visible: true,
        agents: [agent('h1', '0.1.0'), agent('h2', undefined), agent('h3', '0.1.0')],
        hosts: [host('h1'), host('h2'), host('h3')],
      },
      global: { plugins: [installTestI18n()] },
    })
    await vi.waitFor(() => expect(store.getAgentUpdateTarget).toHaveBeenCalled())

    await wrapper.find('[data-test="bulk-update-start"]').trigger('click')
    await vi.waitFor(() => expect(store.updateAgentBinary).toHaveBeenCalledTimes(3))
    await vi.waitFor(() => expect(wrapper.find('[data-test="bulk-update-row-h1"]').text()).toContain('成功'))

    expect(wrapper.find('[data-test="bulk-update-row-h1"]').text()).toContain('成功')
    expect(wrapper.find('[data-test="bulk-update-row-h2"]').text()).toContain('失败')
    expect(wrapper.find('[data-test="bulk-update-row-h2"]').text()).toContain('upload failed')
    expect(wrapper.find('[data-test="bulk-update-row-h3"]').text()).toContain('成功')
    expect(store.loadAgents).toHaveBeenCalled()
  })
})
