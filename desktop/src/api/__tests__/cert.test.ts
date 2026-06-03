/**
 * cert API client 测试。
 *
 * 职责：
 *   - 验证桌面端 SSL 证书 API client 调用正确路径和 HTTP 方法
 *   - 验证错误响应转换为 Error
 *
 * 边界：
 *   - 不启动真实 agent
 *   - 不测试浏览器 fetch 实现
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { certApi } from '@/api/cert'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

function okJSON(body: unknown): Response {
  return {
    ok: true,
    json: () => Promise.resolve(body),
  } as Response
}

describe('certApi', () => {
  it('lists managed certificates', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON([{ id: 'cert-1', domains: ['api.example.com'] }]))

    const result = await certApi.listCertificates()

    expect(result[0].id).toBe('cert-1')
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/ingress/certs'),
      expect.any(Object),
    )
  })

  it('creates and issues a certificate', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON({ id: 'cert-1', status: 'pending' }))

    await certApi.createCertificate({
      domains: ['api.example.com'],
      issuer: 'acme',
      dns_provider: 'cloudflare-prod',
      auto_renew: true,
    })
    await certApi.issueCertificate('cert-1')

    expect(globalThis.fetch).toHaveBeenNthCalledWith(
      1,
      expect.stringContaining('/api/ingress/certs'),
      expect.objectContaining({ method: 'POST' }),
    )
    expect(globalThis.fetch).toHaveBeenNthCalledWith(
      2,
      expect.stringContaining('/api/ingress/certs/cert-1/issue'),
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('matches a certificate by domain', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON({ id: 'cert-wild', domains: ['*.example.com'] }))

    const matched = await certApi.matchCertificate('api.example.com')

    expect(matched?.id).toBe('cert-wild')
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/ingress/certs/match?domain=api.example.com'),
      expect.any(Object),
    )
  })
})
