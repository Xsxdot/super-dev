/**
 * Agent lifecycle stage helper tests.
 *
 * 职责：
 *   - 验证 Agent 列表与统一配置面板共用的生命周期推导
 *   - 覆盖实时 NodeStatus.route 对降级状态的影响
 *
 * 边界：
 *   - 不渲染 Vue 组件
 *   - 不访问 Pinia store 或真实后端
 */
import { describe, expect, it } from 'vitest'
import { agentStage, agentStageView, type AgentStage } from '@/lib/agentStage'
import type { AgentDTO, AgentHealth, NodeStatus } from '@/api/agent'

function agent(runtime: Partial<AgentDTO['runtime']> = {}): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: ['prod'],
    transport: {
      chain: [
        { type: 'direct', direct: { address: '100.64.0.8:57017' } },
        { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
      ],
    },
    config: { listen_address: '127.0.0.1', listen_port: 57017 },
    runtime: {
      installed: true,
      health: 'healthy',
      reachable: true,
      ...runtime,
    },
    security: { token_configured: true, provision_state: 'provisioned', tls: { mode: 'auto' } },
    updated_at: '2026-06-07T10:00:00Z',
  }
}

function node(health: AgentHealth, route?: NodeStatus['route']): NodeStatus {
  return {
    host_id: 'h1',
    name: 'ali-01',
    reachable: health === 'healthy',
    agent: {
      installed: health !== 'unknown',
      health,
      reachable: health === 'healthy',
      version: health === 'healthy' ? '0.1.0' : undefined,
    },
    deployments: [],
    route,
    updated_at: '2026-06-07T10:00:00Z',
  }
}

describe('agentStage', () => {
  it.each<[string, AgentDTO, NodeStatus | undefined, AgentStage]>([
    ['pending install from runtime.installed=false', agent({ installed: false, health: 'unknown', reachable: false }), undefined, 'pending-install'],
    ['version mismatch from runtime health', agent({ health: 'version-mismatch', reachable: false }), undefined, 'version-mismatch'],
    ['auth failure from runtime health', agent({ health: 'auth-failed', reachable: false }), undefined, 'auth-failed'],
    ['unreachable after installed', agent({ health: 'unreachable', reachable: false }), undefined, 'unreachable'],
    ['healthy without degraded route', agent({ health: 'healthy', reachable: true }), node('healthy', { selected_index: 0, selected_type: 'direct', degraded: false }), 'healthy'],
    ['degraded from route.degraded', agent({ health: 'healthy', reachable: true }), node('healthy', { selected_index: 1, selected_type: 'tunnel', degraded: true }), 'degraded'],
    ['degraded from selected fallback index', agent({ health: 'healthy', reachable: true }), node('healthy', { selected_index: 1, selected_type: 'tunnel', degraded: false }), 'degraded'],
  ])('%s', (_name, dto, status, expected) => {
    expect(agentStage(dto, status)).toBe(expected)
  })

  it('prefers realtime node health over stale AgentDTO runtime', () => {
    expect(agentStage(agent({ health: 'unreachable', reachable: false }), node('healthy', {
      selected_index: 0,
      selected_type: 'direct',
      degraded: false,
    }))).toBe('healthy')
  })

  it('maps each stage to the expected primary action and panel tab', () => {
    expect(agentStageView('pending-install')).toMatchObject({ primaryActionKey: 'settings.agents.install', panelTab: 'install', opensPanel: true })
    expect(agentStageView('version-mismatch')).toMatchObject({ primaryActionKey: 'settings.agents.reinstall', panelTab: 'install', opensPanel: true })
    expect(agentStageView('auth-failed')).toMatchObject({ primaryActionKey: 'settings.agents.provisionSecurity', panelTab: 'security', opensPanel: true })
    expect(agentStageView('unreachable')).toMatchObject({ primaryActionKey: 'settings.agents.fixConnection', panelTab: 'transport', opensPanel: true })
    expect(agentStageView('degraded')).toMatchObject({ primaryActionKey: 'settings.agents.fixPrimaryConnection', panelTab: 'transport', opensPanel: true })
    expect(agentStageView('healthy')).toMatchObject({ primaryActionKey: 'settings.agents.recheck', panelTab: null, opensPanel: false })
  })
})
