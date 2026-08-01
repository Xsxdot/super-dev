/**
 * SSL 证书 API client。
 *
 * 职责：
 *   - 封装桌面端对 agent 全局 SSL 证书和 ACME 账号接口的调用
 *   - 暴露托管证书、证书材料、部署记录和 ACME 账号类型
 *
 * 边界：
 *   - 不缓存响应数据，状态由 stores/cert 管理
 *   - 不处理签发轮询或表单校验
 */
import { AGENT_HOST } from '@/api/agent'
import { agentToken, invalidateAgentToken } from '@/lib/agentAuth'

const BASE = `http://${AGENT_HOST}`

class HTTPError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'HTTPError'
    this.status = status
  }
}

async function buildHeaders(): Promise<Record<string, string>> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = await agentToken()
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  // 首次 401：多半是 agent 重启轮换了本机 token——失效缓存重取一次再试，
  // 仍 401 则按既有错误路径抛出。重试静默进行，不打日志（高频路径）。
  let res = await fetch(`${BASE}${path}`, { ...options, headers: await buildHeaders() })
  if (res.status === 401) {
    invalidateAgentToken()
    res = await fetch(`${BASE}${path}`, { ...options, headers: await buildHeaders() })
  }
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      /* 非 JSON 错误体 */
    }
    throw new HTTPError(res.status, message)
  }
  return res.json() as Promise<T>
}

export type CertStatus = 'pending' | 'active' | 'expiring' | 'failed'
export type CertificateIssuer = 'acme' | 'manual'

export interface CertificateMaterial {
  domain: string
  cert_pem: string
  key_pem?: string
  issuer?: string
  obtained_at?: string
  expires_at?: string
  provider?: string
}

export interface CertDeployment {
  host_id: string
  cert_path: string
  key_path: string
  post_deploy_command?: string
  source_type?: string
  source_id?: string
  status?: 'succeeded' | 'failed' | string
  last_error?: string
  deployed_at: string
}

export interface ManagedCertificate {
  id: string
  domains: string[]
  issuer: CertificateIssuer
  dns_provider?: string
  status: CertStatus
  material?: CertificateMaterial
  deployments?: CertDeployment[]
  last_error?: string
  auto_renew: boolean
  created_at?: string
  updated_at?: string
}

export interface CertificateCreatePayload {
  domains: string[]
  issuer: CertificateIssuer
  dns_provider?: string
  auto_renew: boolean
  material?: CertificateMaterial
}

export interface CertificateDeployTargetPayload {
  host_id: string
  cert_path?: string
  key_path?: string
  post_deploy_command?: string
}

export interface CertificateDeployPayload {
  host_ids?: string[]
  cert_path?: string
  key_path?: string
  post_deploy_command?: string
  deployments?: CertificateDeployTargetPayload[]
}

export interface ACMEAccount {
  email: string
  directory_url?: string
  updated_at?: string
}

export const certApi = {
  listCertificates: () => request<ManagedCertificate[]>('/api/ingress/certs'),
  getCertificate: (id: string) =>
    request<ManagedCertificate>(`/api/ingress/certs/${encodeURIComponent(id)}`),
  createCertificate: (payload: CertificateCreatePayload) =>
    request<ManagedCertificate>('/api/ingress/certs', { method: 'POST', body: JSON.stringify(payload) }),
  deleteCertificate: (id: string) =>
    request<{ status: string }>(`/api/ingress/certs/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  issueCertificate: (id: string) =>
    request<ManagedCertificate>(`/api/ingress/certs/${encodeURIComponent(id)}/issue`, { method: 'POST', body: '{}' }),
  renewCertificate: (id: string) =>
    request<ManagedCertificate>(`/api/ingress/certs/${encodeURIComponent(id)}/renew`, { method: 'POST', body: '{}' }),
  deployCertificate: (id: string, payload: CertificateDeployPayload = { host_ids: [] }) =>
    request<ManagedCertificate>(`/api/ingress/certs/${encodeURIComponent(id)}/deploy`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  matchCertificate: async (domain: string) => {
    try {
      return await request<ManagedCertificate>(`/api/ingress/certs/match?domain=${encodeURIComponent(domain)}`)
    } catch (err) {
      if (err instanceof HTTPError && err.status === 404) return null
      throw err
    }
  },
  getACMEAccount: () => request<ACMEAccount>('/api/ingress/acme-account'),
  saveACMEAccount: (payload: ACMEAccount) =>
    request<ACMEAccount>('/api/ingress/acme-account', { method: 'POST', body: JSON.stringify(payload) }),
}
