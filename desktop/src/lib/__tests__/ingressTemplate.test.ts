/**
 * ingressTemplate 测试入口表单到 Raw Template 的生成。
 *
 * 职责：
 *   - 验证 upstream 行会生成 nginx 配置全文
 *   - 验证 WebSocket、超时和 TLS 变量输出稳定
 *
 * 边界：
 *   - 不调用 agent API
 *   - 不校验 nginx 语法
 */
import { describe, expect, it } from 'vitest'
import { buildNginxRawTemplate, type IngressTemplateInput } from '../ingressTemplate'

describe('buildNginxRawTemplate', () => {
  it('renders upstream rows as visible nginx config', () => {
    const input: IngressTemplateInput = {
      domain: 'api.example.com',
      upstreams: [
        { ip: '10.0.0.12', port: 8080 },
        { ip: '10.0.0.13', port: 8080 },
      ],
      websocket: true,
      proxyTimeout: '60s',
      tlsEnabled: false,
    }

    const raw = buildNginxRawTemplate(input)

    expect(raw).toContain('upstream api_example_com_upstream')
    expect(raw).toContain('server 10.0.0.12:8080;')
    expect(raw).toContain('server 10.0.0.13:8080;')
    expect(raw).toContain('server_name api.example.com;')
    expect(raw).toContain('proxy_set_header Upgrade $http_upgrade;')
    expect(raw).toContain('proxy_read_timeout 60s;')
  })

  it('uses nginx provider certificate path variables for TLS', () => {
    const raw = buildNginxRawTemplate({
      domain: 'api.example.com',
      upstreams: [{ ip: '10.0.0.12', port: 8080 }],
      websocket: false,
      proxyTimeout: '45s',
      tlsEnabled: true,
    })

    expect(raw).toContain('listen 443 ssl;')
    expect(raw).toContain('ssl_certificate {{ .CertFullchainPath }};')
    expect(raw).toContain('ssl_certificate_key {{ .CertPrivateKeyPath }};')
  })
})
