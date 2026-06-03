/**
 * Ingress API client。
 *
 * 职责：
 *   - 封装桌面端对 agent Ingress HTTP 接口的调用
 *   - 暴露入口声明、预演、落地状态和 DNS provider 配置类型
 *
 * 边界：
 *   - 不缓存响应数据，状态由 stores/ingress 管理
 *   - 不处理表单默认值或 UI 校验
 */
import { AGENT_HOST } from '@/api/agent'

const BASE = `http://${AGENT_HOST}`

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      /* 非 JSON 错误体 */
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export type RecordType = 'A' | 'AAAA' | 'TXT' | 'CNAME'

export interface DNSRecord {
  id?: string
  type: RecordType
  name: string
  value: string
  ttl?: number
}

export interface DNSConfig {
  provider: string
  records: DNSRecord[]
  record?: DNSRecord
}

export interface SourceHint {
  env_name?: string
  pipeline_id?: string
  role?: string
  service?: string
}

export interface ProxyConfig {
  provider: string
  host_ids: string[]
}

export interface Upstream {
  host_id?: string
  ip: string
  port: number | ''
}

export interface TLSConfig {
  enabled: boolean
  cert_provider?: string
}

export interface LocationOption {
  path: string
  raw: string
}

export interface ProxyOptions {
  websocket?: boolean
  proxy_timeout?: string
  extra_locations?: LocationOption[]
  raw_template?: string
}

export interface Ingress {
  id?: string
  project_id: string
  name: string
  domain: string
  source_hint?: SourceHint
  proxy: ProxyConfig
  upstreams: Upstream[]
  proxy_options?: ProxyOptions
  tls: TLSConfig
  dns: DNSConfig
  created_at?: string
  updated_at?: string

  // Legacy fields remain readable while the UI migrates from global ingress screens.
  host_ids?: string[]
  backend?: string
  proxy_provider?: string
}

export interface DNSValueDecision {
  ok: boolean
  value?: string
  requires_input?: boolean
  requires_confirmation?: boolean
  message?: string
}

export interface PreviewResult {
  ingress: Ingress
  dns_record: DNSRecord
  dns_value_decision: DNSValueDecision
  rendered_config_by_host: Record<string, string>
  manual_instructions?: string[]
}

export interface Certificate {
  domain: string
  cert_pem: string
  key_pem: string
  issuer?: string
  obtained_at: string
  expires_at: string
  provider: string
}

export interface HostState {
  host_id: string
  config_path: string
  cert_paths?: string[]
}

export interface AppliedState {
  ingress_id: string
  records?: DNSRecord[]
  hosts?: HostState[]
  cert?: Certificate
  updated_at?: string
}

export interface OrphanConfig {
  host_id: string
  path: string
  domain: string
}

export interface OrphanReport {
  configs: OrphanConfig[]
  records: DNSRecord[]
}

export interface DNSProviderConfig {
  id?: string
  name: string
  type: 'cloudflare' | 'aliyun'
  zone_id?: string
  secrets?: Record<string, string>
}

export interface ApplyIngressOptions {
  confirmed_dns_value?: string
}

export interface InferDefaultsRequest {
  env_name: string
  pipeline_id: string
  role: string
  proxy_host_ids: string[]
  domain: string
  record_type: RecordType
}

export interface InferDefaultsResult {
  upstreams: Upstream[]
  dns_records: DNSRecord[]
  warnings?: string[]
  requires_port_input: boolean
}

export const ingressApi = {
  listProjectIngresses: (projectId: string) =>
    request<Ingress[]>(`/api/projects/${encodeURIComponent(projectId)}/ingress`),
  createProjectIngress: (projectId: string, payload: Ingress) =>
    request<Ingress>(`/api/projects/${encodeURIComponent(projectId)}/ingress`, { method: 'POST', body: JSON.stringify(payload) }),
  updateProjectIngress: (projectId: string, id: string, payload: Ingress) =>
    request<Ingress>(`/api/projects/${encodeURIComponent(projectId)}/ingress/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteProjectIngress: (projectId: string, id: string) =>
    request<{ status: string }>(`/api/projects/${encodeURIComponent(projectId)}/ingress/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  previewProjectIngress: (projectId: string, id: string) =>
    request<PreviewResult>(`/api/projects/${encodeURIComponent(projectId)}/ingress/${encodeURIComponent(id)}/preview`, { method: 'POST', body: '{}' }),
  applyProjectIngress: (projectId: string, id: string, options: ApplyIngressOptions = {}) =>
    request<AppliedState>(`/api/projects/${encodeURIComponent(projectId)}/ingress/${encodeURIComponent(id)}/apply`, { method: 'POST', body: JSON.stringify(options) }),
  detectProjectOrphans: (projectId: string, id: string) =>
    request<OrphanReport>(`/api/projects/${encodeURIComponent(projectId)}/ingress/${encodeURIComponent(id)}/detect-orphans`, { method: 'POST', body: '{}' }),
  removeProjectOrphans: (projectId: string, id: string, report: OrphanReport) =>
    request<{ status: string }>(`/api/projects/${encodeURIComponent(projectId)}/ingress/${encodeURIComponent(id)}/orphan-removals`, { method: 'POST', body: JSON.stringify(report) }),
  inferDefaults: (projectId: string, payload: InferDefaultsRequest) =>
    request<InferDefaultsResult>(`/api/projects/${encodeURIComponent(projectId)}/ingress/defaults`, { method: 'POST', body: JSON.stringify(payload) }),
  listIngresses: () => request<Ingress[]>('/api/ingress'),
  createIngress: (payload: Ingress) =>
    request<Ingress>('/api/ingress', { method: 'POST', body: JSON.stringify(payload) }),
  updateIngress: (id: string, payload: Ingress) =>
    request<Ingress>(`/api/ingress/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteIngress: (id: string) =>
    request<{ status: string }>(`/api/ingress/${id}`, { method: 'DELETE' }),
  previewIngress: (id: string) =>
    request<PreviewResult>(`/api/ingress/${id}/preview`, { method: 'POST', body: '{}' }),
  applyIngress: (id: string, options: ApplyIngressOptions = {}) =>
    request<AppliedState>(`/api/ingress/${id}/apply`, { method: 'POST', body: JSON.stringify(options) }),
  detectOrphans: (id: string) =>
    request<OrphanReport>(`/api/ingress/${id}/detect-orphans`, { method: 'POST', body: '{}' }),
  removeOrphans: (id: string, report: OrphanReport) =>
    request<{ status: string }>(`/api/ingress/${id}/orphan-removals`, { method: 'POST', body: JSON.stringify(report) }),
  listDNSProviders: () => request<DNSProviderConfig[]>('/api/ingress/providers/dns'),
  upsertDNSProvider: (payload: DNSProviderConfig) =>
    request<DNSProviderConfig>('/api/ingress/providers/dns', { method: 'POST', body: JSON.stringify(payload) }),
  deleteDNSProvider: (id: string) =>
    request<{ status: string }>(`/api/ingress/providers/dns/${id}`, { method: 'DELETE' }),
}
