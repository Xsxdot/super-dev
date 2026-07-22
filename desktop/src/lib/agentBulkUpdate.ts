/**
 * Agent 批量更新辅助函数。
 *
 * 职责：
 *   - 根据 Agent/Host/目标版本生成批量更新行状态
 *   - 比较 Agent 语义化版本
 *   - 以固定并发执行批量更新任务
 *
 * 边界：
 *   - 不访问 Pinia store
 *   - 不渲染 UI
 *   - 不直接调用后端 API
 */
import type { AgentDTO, Host } from '@/api/agent'

export type AgentBulkUpdateEligibility = 'selected-by-default' | 'manual-allowed' | 'disabled'

export interface AgentBulkUpdateRow {
  hostId: string
  hostName: string
  agent: AgentDTO
  host?: Host
  currentVersion: string
  targetVersion: string
  health: string
  eligibility: AgentBulkUpdateEligibility
  selected: boolean
  disabled: boolean
  reasonKey: string
}

export interface AgentBatchResult {
  hostId: string
  ok: boolean
  error?: string
}

// buildBulkUpdateRows 根据当前 Agent/Host 状态生成批量更新候选行。
export function buildBulkUpdateRows(agents: AgentDTO[], hosts: Host[], targetVersion: string): AgentBulkUpdateRow[] {
  const hostMap = new Map(hosts.map(host => [host.id, host]))
  return agents
    .map(agent => classifyAgent(agent, hostMap.get(agent.host_id), targetVersion))
    .sort((a, b) => a.hostName.localeCompare(b.hostName) || a.hostId.localeCompare(b.hostId))
}

// compareAgentVersions 比较 vX.Y.Z 或 X.Y.Z 形式的 agent 版本。
export function compareAgentVersions(current: string | undefined, target: string | undefined): -1 | 0 | 1 | null {
  const a = parseVersion(current)
  const b = parseVersion(target)
  if (!a || !b) return null
  for (let i = 0; i < 3; i += 1) {
    if (a[i] < b[i]) return -1
    if (a[i] > b[i]) return 1
  }
  return 0
}

// runAgentUpdateBatch 按固定并发执行更新，并隔离单个 host 的失败。
export async function runAgentUpdateBatch(
  hostIds: string[],
  concurrency: number,
  update: (hostId: string) => Promise<unknown>,
  check: (hostId: string) => Promise<unknown>,
): Promise<AgentBatchResult[]> {
  const queue = [...hostIds]
  const results: AgentBatchResult[] = []
  const workerCount = Math.max(1, Math.min(Math.max(1, concurrency), queue.length))

  async function worker() {
    while (queue.length > 0) {
      const hostId = queue.shift()
      if (!hostId) continue
      try {
        await update(hostId)
        await check(hostId)
        results.push({ hostId, ok: true })
      } catch (err) {
        results.push({ hostId, ok: false, error: err instanceof Error ? err.message : 'Request failed' })
      }
    }
  }

  await Promise.all(Array.from({ length: workerCount }, () => worker()))
  return hostIds.map(hostId => results.find(result => result.hostId === hostId) ?? { hostId, ok: false, error: 'No result' })
}

function classifyAgent(agent: AgentDTO, host: Host | undefined, targetVersion: string): AgentBulkUpdateRow {
  const currentVersion = agent.runtime.version?.trim() || ''
  const base = {
    hostId: agent.host_id,
    hostName: agent.host_name,
    agent,
    host,
    currentVersion,
    targetVersion,
    health: agent.runtime.health,
  }
  if (!host) return disabledRow(base, 'settings.agents.bulkUpdateReasonMissingHost')
  if (!agent.runtime.installed) return disabledRow(base, 'settings.agents.bulkUpdateReasonNotInstalled')
  if (!hasSSHConfig(host)) return disabledRow(base, 'settings.agents.bulkUpdateReasonMissingSSH')
  if (agent.runtime.health === 'unreachable') {
    return {
      ...base,
      eligibility: 'manual-allowed',
      selected: false,
      disabled: false,
      reasonKey: 'settings.agents.bulkUpdateReasonUnreachable',
    }
  }
  const comparison = compareAgentVersions(currentVersion, targetVersion)
  if (comparison === 0) return disabledRow(base, 'settings.agents.bulkUpdateReasonCurrent')
  if (comparison === null) {
    return {
      ...base,
      eligibility: 'selected-by-default',
      selected: true,
      disabled: false,
      reasonKey: 'settings.agents.bulkUpdateReasonUnknownVersion',
    }
  }
  if (comparison < 0) {
    return {
      ...base,
      eligibility: 'selected-by-default',
      selected: true,
      disabled: false,
      reasonKey: 'settings.agents.bulkUpdateReasonOutdated',
    }
  }
  return disabledRow(base, 'settings.agents.bulkUpdateReasonCurrent')
}

function disabledRow(
  base: Omit<AgentBulkUpdateRow, 'eligibility' | 'selected' | 'disabled' | 'reasonKey'>,
  reasonKey: string,
): AgentBulkUpdateRow {
  return { ...base, eligibility: 'disabled', selected: false, disabled: true, reasonKey }
}

function hasSSHConfig(host: Host): boolean {
  return Boolean(
    host.ssh_host?.trim()
    && host.ssh_user?.trim()
    && host.ssh_credential_configured
    && host.ssh_host_key_fingerprint_configured,
  )
}

function parseVersion(value: string | undefined): [number, number, number] | null {
  const match = value?.trim().match(/^v?(\d+)\.(\d+)\.(\d+)$/)
  if (!match) return null
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}
