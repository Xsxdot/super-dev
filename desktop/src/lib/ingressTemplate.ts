/**
 * Nginx Raw Template 生成器。
 *
 * 职责：
 *   - 根据入口表单结构化字段生成用户可见的 nginx 配置全文
 *   - 保持最终部署源与用户看到的 Raw Template 一致
 *
 * 边界：
 *   - 不调用 agent API
 *   - 不校验证书是否存在
 */
export interface IngressTemplateInput {
  domain: string
  upstreams: Array<{ ip: string; port: number | '' }>
  websocket: boolean
  proxyTimeout: string
  tlsEnabled: boolean
}

export function buildNginxRawTemplate(input: IngressTemplateInput): string {
  const domain = input.domain.trim()
  const upstreamName = upstreamNameForDomain(domain)
  const upstreamLines = input.upstreams
    .filter(row => row.ip.trim() !== '' && row.port !== '')
    .map(row => `    server ${row.ip.trim()}:${row.port};`)
  const websocketHeaders = input.websocket
    ? [
        '        proxy_http_version 1.1;',
        '        proxy_set_header Upgrade $http_upgrade;',
        '        proxy_set_header Connection "upgrade";',
      ]
    : []
  const timeout = input.proxyTimeout.trim() || '60s'
  const listenLines = input.tlsEnabled
    ? [
        '    listen 443 ssl;',
        '    ssl_certificate {{ .CertFullchainPath }};',
        '    ssl_certificate_key {{ .CertPrivateKeyPath }};',
      ]
    : ['    listen 80;']

  return [
    `upstream ${upstreamName} {`,
    ...upstreamLines,
    '}',
    '',
    'server {',
    ...listenLines,
    `    server_name ${domain};`,
    '',
    '    location / {',
    `        proxy_pass http://${upstreamName};`,
    '        proxy_set_header Host $host;',
    '        proxy_set_header X-Real-IP $remote_addr;',
    '        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;',
    '        proxy_set_header X-Forwarded-Proto $scheme;',
    `        proxy_read_timeout ${timeout};`,
    `        proxy_send_timeout ${timeout};`,
    ...websocketHeaders,
    '    }',
    '}',
    '',
  ].join('\n')
}

function upstreamNameForDomain(domain: string): string {
  const normalized = domain.replace(/[^A-Za-z0-9]+/g, '_').replace(/^_+|_+$/g, '')
  return `${normalized || 'ingress'}_upstream`
}
