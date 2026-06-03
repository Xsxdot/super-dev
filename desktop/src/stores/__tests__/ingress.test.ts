/**
 * ingress store 测试入口配置状态管理。
 *
 * 职责：
 *   - 验证入口声明、DNS provider 和预演结果会同步到 Pinia state
 *   - 验证保存入口时本地列表会追加或替换对应声明
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不渲染 Ingress 管理组件
 */
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useIngressStore } from '@/stores/ingress'
import { ingressApi, type Ingress } from '@/api/ingress'

vi.mock('@/api/ingress', async () => {
  const actual = await vi.importActual<typeof import('@/api/ingress')>('@/api/ingress')
  return {
    ...actual,
    ingressApi: {
      listIngresses: vi.fn(),
      createIngress: vi.fn(),
      updateIngress: vi.fn(),
      deleteIngress: vi.fn(),
      previewIngress: vi.fn(),
      applyIngress: vi.fn(),
      detectOrphans: vi.fn(),
      removeOrphans: vi.fn(),
      listDNSProviders: vi.fn(),
      upsertDNSProvider: vi.fn(),
      deleteDNSProvider: vi.fn(),
    },
  }
})

const mockedApi = ingressApi as unknown as Record<string, Mock>

function makeIngress(overrides: Partial<Ingress> = {}): Ingress {
  return {
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
    ...overrides,
  }
}

describe('useIngressStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('loadAll loads ingress declarations and DNS providers', async () => {
    mockedApi.listIngresses.mockResolvedValue([makeIngress()])
    mockedApi.listDNSProviders.mockResolvedValue([
      { id: 'cloudflare-prod', name: 'Cloudflare', type: 'cloudflare' },
    ])
    const store = useIngressStore()

    await store.loadAll()

    expect(store.ingresses).toHaveLength(1)
    expect(store.dnsProviders[0].id).toBe('cloudflare-prod')
  })

  it('saveIngress appends newly created ingress', async () => {
    mockedApi.createIngress.mockResolvedValue(makeIngress({ id: 'ing-new' }))
    const store = useIngressStore()

    await store.saveIngress(makeIngress({ id: undefined }))

    expect(store.ingresses[0].id).toBe('ing-new')
    expect(mockedApi.createIngress).toHaveBeenCalled()
  })

  it('previewIngress stores preview result by ingress id', async () => {
    mockedApi.previewIngress.mockResolvedValue({
      ingress: makeIngress(),
      dns_record: { type: 'A', name: 'api.example.com', value: '203.0.113.10' },
      dns_value_decision: { ok: true, value: '203.0.113.10' },
      rendered_config_by_host: { self: 'server_name api.example.com;' },
    })
    const store = useIngressStore()

    await store.previewIngress('ing-1')

    expect(store.previews['ing-1']?.rendered_config_by_host.self).toContain('api.example.com')
  })
})
