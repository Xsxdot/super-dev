/**
 * Agent 生命周期阶段推导。
 *
 * 职责：
 *   - 将 AgentDTO 与实时 NodeStatus 归一成前端生命周期阶段
 *   - 为列表主按钮提供稳定的文案 key、样式和默认面板页签
 *
 * 边界：
 *   - 不读取 Pinia store
 *   - 不调用 Agent API
 *   - 不格式化具体连接地址
 */
import type { AgentDTO, AgentRuntime, NodeStatus } from '@/api/agent'

export type AgentStage =
  | 'pending-install'
  | 'unreachable'
  | 'degraded'
  | 'version-mismatch'
  | 'healthy'
  | 'auth-failed'
  | 'pending-bootstrap'

export type AgentPanelTab = 'security' | 'install' | 'transport' | 'probe'

export interface AgentStageView {
  stage: AgentStage
  labelKey: string
  primaryActionKey: string
  panelTab: AgentPanelTab | null
  tone: 'success' | 'warning' | 'danger'
  primary: boolean
  opensPanel: boolean
}

const knownAgentHealth: Record<AgentRuntime['health'], true> = {
  unknown: true,
  healthy: true,
  unreachable: true,
  'version-mismatch': true,
  'auth-failed': true,
  'pending-bootstrap': true,
}

// runtime health may be empty immediately after creating an Agent, before
// NodeRegistry has produced the first probe snapshot. Treat that as unknown
// so render paths never pass an undefined translation key to vue-i18n.
function normalizeAgentHealth(health: AgentRuntime['health'] | string | undefined): AgentRuntime['health'] {
  return knownAgentHealth[health as AgentRuntime['health']] ? health as AgentRuntime['health'] : 'unknown'
}

// runtimeFor returns the freshest Agent runtime snapshot with defensive
// normalization for newly-created rows that have not been probed yet.
export function runtimeFor(agent: AgentDTO, node?: NodeStatus): AgentRuntime {
  const runtime = node?.agent ?? agent.runtime
  return {
    ...runtime,
    health: normalizeAgentHealth(runtime?.health),
    installed: Boolean(runtime?.installed),
    reachable: Boolean(runtime?.reachable),
  }
}

export function agentStage(agent: AgentDTO, node?: NodeStatus): AgentStage {
  const runtime = runtimeFor(agent, node)
  const route = node?.route

  if (runtime.installed === false && runtime.health !== 'healthy') {
    return 'pending-install'
  }
  if (runtime.health === 'version-mismatch') {
    return 'version-mismatch'
  }
  if (runtime.health === 'auth-failed') {
    return 'auth-failed'
  }
  if (runtime.health === 'pending-bootstrap') {
    return 'pending-bootstrap'
  }
  if (runtime.health === 'unreachable') {
    return 'unreachable'
  }
  if (runtime.health === 'healthy' && (route?.degraded || (route?.selected_index ?? 0) > 0)) {
    return 'degraded'
  }
  if (runtime.health === 'healthy') {
    return 'healthy'
  }

  return runtime.installed ? 'unreachable' : 'pending-install'
}

export function agentStageView(stage: AgentStage): AgentStageView {
  switch (stage) {
    case 'pending-install':
      return {
        stage,
        labelKey: 'settings.agents.stagePendingInstall',
        primaryActionKey: 'settings.agents.install',
        panelTab: 'install',
        tone: 'warning',
        primary: true,
        opensPanel: true,
      }
    case 'version-mismatch':
      return {
        stage,
        labelKey: 'settings.agents.stageVersionMismatch',
        primaryActionKey: 'settings.agents.reinstall',
        panelTab: 'install',
        tone: 'warning',
        primary: true,
        opensPanel: true,
      }
    case 'auth-failed':
      return {
        stage,
        labelKey: 'settings.agents.stageAuthFailed',
        primaryActionKey: 'settings.agents.provisionSecurity',
        panelTab: 'security',
        tone: 'warning',
        primary: true,
        opensPanel: true,
      }
    case 'pending-bootstrap':
      return {
        stage,
        labelKey: 'settings.agents.stagePendingBootstrap',
        primaryActionKey: 'settings.agents.provisionSecurity',
        panelTab: 'security',
        tone: 'warning',
        primary: true,
        opensPanel: true,
      }
    case 'unreachable':
      return {
        stage,
        labelKey: 'settings.agents.stageUnreachable',
        primaryActionKey: 'settings.agents.fixConnection',
        panelTab: 'transport',
        tone: 'danger',
        primary: true,
        opensPanel: true,
      }
    case 'degraded':
      return {
        stage,
        labelKey: 'settings.agents.stageDegraded',
        primaryActionKey: 'settings.agents.fixPrimaryConnection',
        panelTab: 'transport',
        tone: 'success',
        primary: true,
        opensPanel: true,
      }
    case 'healthy':
      return {
        stage,
        labelKey: 'settings.agents.stageHealthy',
        primaryActionKey: 'settings.agents.recheck',
        panelTab: null,
        tone: 'success',
        primary: false,
        opensPanel: false,
      }
  }
}
