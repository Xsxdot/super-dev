/**
 * Agent bind/address helper tests.
 *
 * 职责：
 *   - 验证前端安装预览使用与后端一致的 bind 推导规则
 *   - 验证 direct 地址候选来自 Host 地址元数据并按推荐顺序去重
 *
 * 边界：
 *   - 不渲染 Vue 组件
 *   - 不访问 Pinia store 或后端 API
 */
import { describe, expect, it } from 'vitest'
import {
  LOOPBACK_BIND_ADDRESS,
  PUBLIC_BIND_ADDRESS,
  bindReasonFromChain,
  directAddressOptions,
  formatHostPort,
  recommendedDirectAddress,
  resolveBindAddressFromChain,
} from '@/lib/agentBind'
import type { Host, TransportEntry } from '@/api/agent'

const tunnelOnly: TransportEntry[] = [{ type: 'tunnel', tunnel: { remote_agent_port: 57017 } }]
const directOnly: TransportEntry[] = [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }]

describe('resolveBindAddressFromChain', () => {
  it('uses loopback for empty or tunnel-only chains', () => {
    expect(resolveBindAddressFromChain([])).toBe(LOOPBACK_BIND_ADDRESS)
    expect(resolveBindAddressFromChain(tunnelOnly)).toBe(LOOPBACK_BIND_ADDRESS)
    expect(bindReasonFromChain(tunnelOnly)).toBe('loopback')
  })

  it('uses public bind when any direct entry exists', () => {
    expect(resolveBindAddressFromChain(directOnly)).toBe(PUBLIC_BIND_ADDRESS)
    expect(resolveBindAddressFromChain([...tunnelOnly, ...directOnly])).toBe(PUBLIC_BIND_ADDRESS)
    expect(bindReasonFromChain(directOnly)).toBe('direct')
  })
})

describe('directAddressOptions', () => {
  it('orders public, private, then ssh host and removes duplicate host values', () => {
    const host: Host = {
      id: 'h1',
      name: 'ali-01',
      public_ip: '203.0.113.10',
      private_ip: '10.0.0.8',
      ssh_host: '10.0.0.8',
      tags: [],
    }

    expect(directAddressOptions(host, 57019)).toEqual([
      { key: 'public_ip', labelKey: 'settings.agents.directAddressPublic', host: '203.0.113.10', address: '203.0.113.10:57019' },
      { key: 'private_ip', labelKey: 'settings.agents.directAddressPrivate', host: '10.0.0.8', address: '10.0.0.8:57019' },
    ])
  })

  it('recommends public IP first, then private IP, then SSH host', () => {
    expect(recommendedDirectAddress({ public_ip: '203.0.113.10', private_ip: '10.0.0.8' }, 57017)).toBe('203.0.113.10:57017')
    expect(recommendedDirectAddress({ private_ip: '10.0.0.8', ssh_host: 'ssh.internal' }, 57017)).toBe('10.0.0.8:57017')
    expect(recommendedDirectAddress({ ssh_host: 'ssh.internal' }, 57017)).toBe('ssh.internal:57017')
    expect(recommendedDirectAddress(undefined, 57017)).toBe('')
  })

  it('formats IPv6 direct options with brackets', () => {
    expect(formatHostPort('2001:db8::10', 57017)).toBe('[2001:db8::10]:57017')
    expect(formatHostPort('[2001:db8::10]', 57017)).toBe('[2001:db8::10]:57017')
  })
})
