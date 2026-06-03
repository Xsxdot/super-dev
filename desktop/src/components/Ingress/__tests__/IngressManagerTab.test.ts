/**
 * IngressManagerTab 测试入口配置管理界面。
 *
 * 职责：
 *   - 验证空态和新建表单可见
 *   - 验证保存入口声明会调用 ingress store action
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不执行真实 preview、apply、DNS 或 nginx 操作
 */
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import IngressManagerTab from '@/components/Ingress/IngressManagerTab.vue'
import { ingressApi } from '@/api/ingress'
import { api } from '@/api/agent'
import { useIngressStore } from '@/stores/ingress'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/api/ingress', async () => {
  const actual = await vi.importActual<typeof import('@/api/ingress')>('@/api/ingress')
  return {
    ...actual,
    ingressApi: {
      listIngresses: vi.fn().mockResolvedValue([]),
      createIngress: vi.fn(),
      updateIngress: vi.fn(),
      deleteIngress: vi.fn(),
      previewIngress: vi.fn(),
      applyIngress: vi.fn(),
      detectOrphans: vi.fn(),
      removeOrphans: vi.fn(),
      listDNSProviders: vi.fn().mockResolvedValue([]),
      upsertDNSProvider: vi.fn(),
      deleteDNSProvider: vi.fn(),
    },
  }
})

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([
        {
          id: 'self',
          name: 'Local',
          ssh_host: '127.0.0.1',
          ssh_port: 22,
          ssh_user: '',
          remote_agent_port: 57017,
          local_tunnel_port: 0,
          tags: [],
          is_self: true,
        },
      ]),
    },
  }
})

const mockedIngressApi = ingressApi as unknown as Record<string, Mock>
const mockedAgentApi = api as unknown as Record<string, Mock>

describe('IngressManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedIngressApi.listIngresses.mockResolvedValue([])
    mockedIngressApi.listDNSProviders.mockResolvedValue([])
    mockedAgentApi.listHosts.mockResolvedValue([
      {
        id: 'self',
        name: 'Local',
        ssh_host: '127.0.0.1',
        ssh_port: 22,
        ssh_user: '',
        remote_agent_port: 57017,
        local_tunnel_port: 0,
        tags: [],
        is_self: true,
      },
    ])
  })

  it('renders empty state after loading', async () => {
    const wrapper = mount(IngressManagerTab, { global: { plugins: [installTestI18n()] } })
    await flush()

    expect(wrapper.text()).toContain('暂无入口配置')
    expect(wrapper.find('[data-test="ingress-add"]').exists()).toBe(true)
  })

  it('opens create form and saves manual DNS ingress', async () => {
    mockedIngressApi.createIngress.mockResolvedValue({
      id: 'ing-1',
      name: 'api',
      domain: 'api.example.com',
      host_ids: ['self'],
      backend: '127.0.0.1:8080',
      proxy_provider: 'nginx',
      dns: {
        provider: 'manual',
        record: { type: 'A', name: 'api.example.com', value: '203.0.113.10' },
      },
      tls: { enabled: false },
    })
    const wrapper = mount(IngressManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useIngressStore()
    const saveSpy = vi.spyOn(store, 'saveIngress')
    await flush()

    await wrapper.find('[data-test="ingress-add"]').trigger('click')
    await wrapper.find('[data-test="ingress-name"]').setValue('api')
    await wrapper.find('[data-test="ingress-domain"]').setValue('api.example.com')
    await wrapper.find('[data-test="ingress-backend"]').setValue('127.0.0.1:8080')
    await wrapper.find('[data-test="ingress-dns-value"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="ingress-save"]').trigger('click')

    expect(saveSpy).toHaveBeenCalledWith(expect.objectContaining({
      domain: 'api.example.com',
      host_ids: ['self'],
      backend: '127.0.0.1:8080',
      dns: expect.objectContaining({
        provider: 'manual',
        record: expect.objectContaining({ value: '203.0.113.10' }),
      }),
    }))
  })
})

function flush() {
  return new Promise(resolve => setTimeout(resolve))
}
