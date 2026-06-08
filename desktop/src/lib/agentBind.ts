/**
 * Agent bind 地址和 direct 地址候选推导。
 *
 * 职责：
 *   - 在前端安装预览中镜像后端的 bind 自动推导规则
 *   - 从 Host 地址元数据生成 direct 连接候选
 *
 * 边界：
 *   - 不保存用户选择
 *   - 不访问 Pinia store
 *   - 不执行网络探测
 */
import type { Host, TransportEntry } from '@/api/agent'

export const LOOPBACK_BIND_ADDRESS = '127.0.0.1'
export const PUBLIC_BIND_ADDRESS = '0.0.0.0'

export type BindReason = 'direct' | 'loopback'
export type DirectAddressSource = 'public_ip' | 'private_ip' | 'ssh_host'

export interface DirectAddressOption {
  key: DirectAddressSource
  labelKey: string
  host: string
  address: string
}

export function chainHasDirect(chain: TransportEntry[] = []): boolean {
  return chain.some(entry => entry.type === 'direct')
}

export function resolveBindAddressFromChain(chain: TransportEntry[] = []): string {
  return chainHasDirect(chain) ? PUBLIC_BIND_ADDRESS : LOOPBACK_BIND_ADDRESS
}

export function bindReasonFromChain(chain: TransportEntry[] = []): BindReason {
  return chainHasDirect(chain) ? 'direct' : 'loopback'
}

export function directAddressOptions(host: Pick<Host, 'public_ip' | 'private_ip' | 'ssh_host'> | null | undefined, port: number): DirectAddressOption[] {
  const seen = new Set<string>()
  const candidates: Array<{ key: DirectAddressSource; labelKey: string; host?: string }> = [
    { key: 'public_ip', labelKey: 'settings.agents.directAddressPublic', host: host?.public_ip },
    { key: 'private_ip', labelKey: 'settings.agents.directAddressPrivate', host: host?.private_ip },
    { key: 'ssh_host', labelKey: 'settings.agents.directAddressSSH', host: host?.ssh_host },
  ]

  const out: DirectAddressOption[] = []
  for (const candidate of candidates) {
    const value = candidate.host?.trim()
    if (!value || seen.has(value)) continue
    seen.add(value)
    out.push({
      key: candidate.key,
      labelKey: candidate.labelKey,
      host: value,
      address: formatHostPort(value, port),
    })
  }
  return out
}

export function recommendedDirectAddress(host: Pick<Host, 'public_ip' | 'private_ip' | 'ssh_host'> | null | undefined, port: number): string {
  return directAddressOptions(host, port)[0]?.address ?? ''
}

export function formatHostPort(host: string, port: number): string {
  const value = host.trim()
  if (!value) return ''
  const normalizedPort = Number(port) || 57017
  if (value.startsWith('[') && value.endsWith(']')) return `${value}:${normalizedPort}`
  if (value.includes(':')) return `[${value}]:${normalizedPort}`
  return `${value}:${normalizedPort}`
}
