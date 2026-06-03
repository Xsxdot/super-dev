/**
 * CertificateTab 测试设置页全局 SSL 证书管理。
 *
 * 职责：
 *   - 验证 ACME 账号保存、证书创建签发和证书部署交互
 *   - 验证组件通过 cert、ingress、remote store 协作加载基础数据
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不测试后端 ACME、DNS 或远端部署细节
 */
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import CertificateTab from '@/components/Settings/CertificateTab.vue'
import { certApi } from '@/api/cert'
import { ingressApi } from '@/api/ingress'
import { api } from '@/api/agent'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/api/cert', async () => {
  const actual = await vi.importActual<typeof import('@/api/cert')>('@/api/cert')
  return {
    ...actual,
    certApi: {
      listCertificates: vi.fn(),
      getACMEAccount: vi.fn(),
      saveACMEAccount: vi.fn(),
      createCertificate: vi.fn(),
      issueCertificate: vi.fn(),
      getCertificate: vi.fn(),
      renewCertificate: vi.fn(),
      deployCertificate: vi.fn(),
      deleteCertificate: vi.fn(),
      matchCertificate: vi.fn(),
    },
  }
})

vi.mock('@/api/ingress', async () => {
  const actual = await vi.importActual<typeof import('@/api/ingress')>('@/api/ingress')
  return {
    ...actual,
    ingressApi: {
      ...actual.ingressApi,
      listDNSProviders: vi.fn(),
    },
  }
})

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn(),
    },
  }
})

const certMock = certApi as unknown as Record<string, Mock>
const ingressMock = ingressApi as unknown as Record<string, Mock>
const agentMock = api as unknown as Record<string, Mock>

describe('CertificateTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    certMock.listCertificates.mockResolvedValue([])
    certMock.getACMEAccount.mockResolvedValue({ email: '', directory_url: '' })
    ingressMock.listDNSProviders.mockResolvedValue([{ id: 'cloudflare-prod', name: 'Cloudflare Prod', type: 'cloudflare' }])
    agentMock.listHosts.mockResolvedValue([{ id: 'edge-a', name: 'edge-a', ssh_host: 'edge', ssh_port: 22, ssh_user: 'root', tags: [] }])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('saves the ACME account', async () => {
    certMock.saveACMEAccount.mockResolvedValue({ email: 'ops@example.com', directory_url: '' })
    const wrapper = mount(CertificateTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flush()

    await wrapper.find('[data-test="acme-email"]').setValue('ops@example.com')
    await wrapper.find('[data-test="acme-save"]').trigger('click')

    expect(certMock.saveACMEAccount).toHaveBeenCalledWith(expect.objectContaining({ email: 'ops@example.com' }))
  })

  it('creates and issues an ACME certificate', async () => {
    certMock.createCertificate.mockResolvedValue({ id: 'cert-1', domains: ['api.example.com'], issuer: 'acme', status: 'pending', auto_renew: true })
    certMock.issueCertificate.mockResolvedValue({ id: 'cert-1', domains: ['api.example.com'], issuer: 'acme', status: 'pending', auto_renew: true })
    certMock.getCertificate.mockResolvedValue({ id: 'cert-1', domains: ['api.example.com'], issuer: 'acme', status: 'active', auto_renew: true })
    const wrapper = mount(CertificateTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flush()
    vi.useFakeTimers()

    await wrapper.find('[data-test="cert-add"]').trigger('click')
    await wrapper.find('[data-test="cert-domain-0"]').setValue('api.example.com')
    await wrapper.find('[data-test="cert-dns-provider"]').setValue('cloudflare-prod')
    await wrapper.find('[data-test="cert-submit"]').trigger('click')
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(1000)

    expect(certMock.createCertificate).toHaveBeenCalledWith(expect.objectContaining({
      domains: ['api.example.com'],
      issuer: 'acme',
      dns_provider: 'cloudflare-prod',
      auto_renew: true,
    }))
    expect(certMock.issueCertificate).toHaveBeenCalledWith('cert-1')
  })

  it('deploys selected certificate to selected hosts', async () => {
    certMock.listCertificates.mockResolvedValue([{ id: 'cert-1', domains: ['api.example.com'], issuer: 'acme', status: 'active', auto_renew: true, deployments: [] }])
    certMock.deployCertificate.mockResolvedValue({ id: 'cert-1', domains: ['api.example.com'], issuer: 'acme', status: 'active', auto_renew: true, deployments: [{ host_id: 'edge-a' }] })
    const wrapper = mount(CertificateTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flush()

    await wrapper.find('[data-test="cert-deploy-cert-1"]').trigger('click')
    await wrapper.find('[data-test="cert-deploy-host-edge-a"]').setValue(true)
    await wrapper.find('[data-test="cert-deploy-submit"]').trigger('click')

    expect(certMock.deployCertificate).toHaveBeenCalledWith('cert-1', ['edge-a'])
  })
})

function flush() {
  return new Promise(resolve => setTimeout(resolve))
}
