/**
 * cert store 测试证书状态管理。
 *
 * 职责：
 *   - 验证托管证书和 ACME 账号会同步到 Pinia state
 *   - 验证签发任务会轮询证书状态直到进入终态
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不渲染 SSL 证书管理组件
 */
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useCertStore } from '@/stores/cert'
import { certApi } from '@/api/cert'

vi.mock('@/api/cert', async () => {
  const actual = await vi.importActual<typeof import('@/api/cert')>('@/api/cert')
  return {
    ...actual,
    certApi: {
      listCertificates: vi.fn(),
      getCertificate: vi.fn(),
      createCertificate: vi.fn(),
      deleteCertificate: vi.fn(),
      issueCertificate: vi.fn(),
      renewCertificate: vi.fn(),
      deployCertificate: vi.fn(),
      matchCertificate: vi.fn(),
      getACMEAccount: vi.fn(),
      saveACMEAccount: vi.fn(),
    },
  }
})

const mockedApi = certApi as unknown as Record<string, Mock>

describe('useCertStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('loads certificates and ACME account', async () => {
    mockedApi.listCertificates.mockResolvedValue([{ id: 'cert-1', domains: ['api.example.com'], status: 'active' }])
    mockedApi.getACMEAccount.mockResolvedValue({ email: 'ops@example.com', directory_url: '' })
    const store = useCertStore()

    await store.loadAll()

    expect(store.certificates[0].id).toBe('cert-1')
    expect(store.acmeAccount.email).toBe('ops@example.com')
  })

  it('normalizes null certificate lists before local upserts', async () => {
    mockedApi.listCertificates.mockResolvedValue(null)
    mockedApi.getACMEAccount.mockResolvedValue({ email: '', directory_url: '' })
    mockedApi.createCertificate.mockResolvedValue({
      id: 'cert-1',
      domains: ['api.example.com'],
      issuer: 'acme',
      status: 'pending',
      auto_renew: true,
    })
    const store = useCertStore()

    await store.loadAll()
    const saved = await store.createCertificate({
      domains: ['api.example.com'],
      issuer: 'acme',
      dns_provider: 'cloudflare-prod',
      auto_renew: true,
    })

    expect(saved.id).toBe('cert-1')
    expect(store.certificates).toHaveLength(1)
  })

  it('polls issue result until active', async () => {
    vi.useFakeTimers()
    mockedApi.issueCertificate.mockResolvedValue({ id: 'cert-1', domains: ['api.example.com'], status: 'pending' })
    mockedApi.getCertificate
      .mockResolvedValueOnce({ id: 'cert-1', domains: ['api.example.com'], status: 'pending' })
      .mockResolvedValueOnce({ id: 'cert-1', domains: ['api.example.com'], status: 'active' })
    const store = useCertStore()

    const promise = store.issueCertificate('cert-1', { intervalMs: 10, maxAttempts: 3 })
    await vi.advanceTimersByTimeAsync(25)
    const result = await promise

    expect(result.status).toBe('active')
  })
})
