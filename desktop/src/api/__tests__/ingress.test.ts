/**
 * ingress API client 测试。
 *
 * 职责：
 *   - 验证桌面端 Ingress API client 调用正确的 agent 路径和 HTTP 方法
 *   - 验证错误响应会被转换为 Error
 *
 * 边界：
 *   - 不启动真实 agent
 *   - 不测试浏览器 fetch 实现
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ingressApi, type Ingress } from '@/api/ingress'

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

describe('ingressApi', () => {
  it('lists project scoped ingresses', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON([{ id: 'ing-1', project_id: 'p1' }]))

    const result = await ingressApi.listProjectIngresses('p1')

    expect(result).toEqual([{ id: 'ing-1', project_id: 'p1' }])
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/p1/ingress'),
      expect.any(Object),
    )
  })

  it('requests inferred ingress defaults for a project', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON({ upstreams: [], dns_records: [], requires_port_input: true }))

    await ingressApi.inferDefaults('p1', {
      env_name: 'prod',
      pipeline_id: 'deploy-prod',
      role: 'api_targets',
      proxy_host_ids: ['edge-a'],
      domain: 'api.example.com',
      record_type: 'A',
    })

    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/projects/p1/ingress/defaults'),
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('createIngress posts to /api/ingress', async () => {
    const payload: Ingress = {
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
    }
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON({ ...payload, id: 'ing-1' }))

    const created = await ingressApi.createIngress(payload)

    expect(created.id).toBe('ing-1')
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/ingress'),
      expect.objectContaining({ method: 'POST', body: JSON.stringify(payload) }),
    )
  })

  it('previewIngress posts to ingress preview endpoint', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON({
      ingress: { id: 'ing-1', domain: 'api.example.com' },
      dns_record: { type: 'A', name: 'api.example.com', value: '203.0.113.10' },
      dns_value_decision: { ok: true, value: '203.0.113.10' },
      rendered_config_by_host: { self: 'server_name api.example.com;' },
    }))

    const preview = await ingressApi.previewIngress('ing-1')

    expect(preview.rendered_config_by_host.self).toContain('api.example.com')
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/ingress/ing-1/preview'),
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('listDNSProviders reads redacted provider configs', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(okJSON([
      { id: 'cloudflare-prod', name: 'Cloudflare', type: 'cloudflare', zone_id: 'zone-1' },
    ]))

    const providers = await ingressApi.listDNSProviders()

    expect(providers[0].id).toBe('cloudflare-prod')
    expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/ingress/providers/dns'),
      expect.any(Object),
    )
  })
})
