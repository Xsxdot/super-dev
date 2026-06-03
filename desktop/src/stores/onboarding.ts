/**
 * onboardingStore 管理零操作引导页状态。
 *
 * 职责：
 *   - 维护用户选择的编程智能体
 *   - 调用 MCP 安装 command 并保存结果
 *   - 提供可复制给 AI 的启动提示词
 *
 * 边界：
 *   - 不直接读写 agent settings
 *   - 不渲染页面
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { i18n } from '@/i18n'
import {
  detectCodingAgents,
  getMcpInstallHint,
  installMcp,
  type CodingAgentAvailability,
  type CodingAgent,
  type InstallHint,
  type InstallOutcome,
} from '@/api/mcpInstall'

export const codingAgents: Array<{ id: CodingAgent; label: string }> = [
  { id: 'claude-code', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
  { id: 'cursor', label: 'Cursor' },
]

interface AgentStatus {
  agent: CodingAgent
  installed: boolean | null
  detection_path?: string | null
}

type AgentStatusMap = Record<CodingAgent, AgentStatus>

interface InstallFailure {
  agent: CodingAgent
  error: string
  hint: InstallHint | null
}

function createAgentStatuses(installed: boolean | null): AgentStatusMap {
  return codingAgents.reduce((statuses, agent) => {
    statuses[agent.id] = {
      agent: agent.id,
      installed,
      detection_path: null,
    }
    return statuses
  }, {} as AgentStatusMap)
}

function agentLabel(agent: CodingAgent): string {
  return codingAgents.find((item) => item.id === agent)?.label ?? agent
}

export const useOnboardingStore = defineStore('onboarding', () => {
  const selectedAgents = ref<CodingAgent[]>([])
  const agentStatuses = ref<AgentStatusMap>(createAgentStatuses(null))
  const detectingAgents = ref(false)
  const detectionError = ref('')
  const installing = ref(false)
  const installOutcomes = ref<InstallOutcome[]>([])
  const installFailures = ref<InstallFailure[]>([])
  const installOutcome = computed(() => installOutcomes.value[0] ?? null)
  const installHint = computed(() => installFailures.value[0]?.hint ?? null)
  const installError = computed(() =>
    installFailures.value.map((failure) => `${agentLabel(failure.agent)}: ${failure.error}`).join('；')
  )

  const demoPrompt = computed(() => {
    // 显式读取 locale，让复制内容和页面语言保持同一个响应式来源。
    i18n.global.locale.value
    return i18n.global.t('onboarding.demoPrompt')
  })

  function isAgentInstalled(agent: CodingAgent) {
    return agentStatuses.value[agent]?.installed === true
  }

  function isAgentSelected(agent: CodingAgent) {
    return selectedAgents.value.includes(agent)
  }

  function agentAvailabilityLabel(agent: CodingAgent) {
    const status = agentStatuses.value[agent]
    if (detectingAgents.value && status?.installed === null) {
      return '检测中'
    }
    if (status?.installed === true) {
      return '已检测到'
    }
    if (detectionError.value && status?.installed === null) {
      return '检测失败'
    }
    return '未检测到'
  }

  function toggleAgentSelection(agent: CodingAgent) {
    if (!isAgentInstalled(agent)) {
      return
    }
    if (selectedAgents.value.includes(agent)) {
      selectedAgents.value = selectedAgents.value.filter((item) => item !== agent)
      return
    }
    selectedAgents.value = [...selectedAgents.value, agent]
  }

  async function detectInstalledAgents() {
    detectingAgents.value = true
    detectionError.value = ''
    try {
      const result = await detectCodingAgents()
      const next = createAgentStatuses(false)
      for (const status of result) {
        next[status.agent] = normalizeAgentStatus(status)
      }
      agentStatuses.value = next
      const installedAgents = codingAgents
        .filter((agent) => next[agent.id].installed === true)
        .map((agent) => agent.id)
      const preservedSelection = selectedAgents.value.filter((agent) => next[agent].installed === true)
      selectedAgents.value = preservedSelection.length > 0 ? preservedSelection : installedAgents
    } catch (error) {
      detectionError.value = error instanceof Error ? error.message : String(error)
      agentStatuses.value = createAgentStatuses(false)
      selectedAgents.value = []
    } finally {
      detectingAgents.value = false
    }
  }

  async function installSelectedMcp() {
    installing.value = true
    installOutcomes.value = []
    installFailures.value = []
    try {
      const agents = selectedAgents.value.filter((agent) => isAgentInstalled(agent))
      for (const agent of agents) {
        try {
          installOutcomes.value.push(await installMcp(agent))
        } catch (error) {
          installFailures.value.push({
            agent,
            error: error instanceof Error ? error.message : String(error),
            hint: await getMcpInstallHint(agent).catch(() => null),
          })
        }
      }
    } finally {
      installing.value = false
    }
  }

  return {
    selectedAgents,
    agentStatuses,
    detectingAgents,
    detectionError,
    installing,
    installOutcome,
    installOutcomes,
    installHint,
    installError,
    installFailures,
    demoPrompt,
    detectInstalledAgents,
    isAgentInstalled,
    isAgentSelected,
    agentAvailabilityLabel,
    toggleAgentSelection,
    installSelectedMcp,
  }
})

function normalizeAgentStatus(status: CodingAgentAvailability): AgentStatus {
  return {
    agent: status.agent,
    installed: status.installed,
    detection_path: status.detection_path ?? null,
  }
}
