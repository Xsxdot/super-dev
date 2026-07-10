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
import { emitConnectorDiagnostic } from '@/lib/connectorDiagnostics'
import { isOnboardingPreviewMode, previewConnectorSummaries, previewConnectorOutcome } from '@/dev/onboardingPreview'
import {
  getAgentConnectorManualInstructions,
  installAgentConnector,
  listAgentConnectors,
  type AgentConnectorSummary,
  type ConnectorId,
  type ConnectorManualInstructions,
  type ConnectorOperationOutcome,
} from '@/api/mcpInstall'

interface InstallFailure {
  agent: ConnectorId
  error: string
  hint: ConnectorManualInstructions | null
}

/** hasWorkingMcp determines onboarding progress from the MCP capability only. */
export function hasWorkingMcp(outcome: ConnectorOperationOutcome): boolean {
  return outcome.integrations.some(integration =>
    integration.capability === 'mcp'
    && (integration.result === 'installed' || integration.result === 'already_present'),
  )
}

export const useOnboardingStore = defineStore('onboarding', () => {
  const connectors = ref<AgentConnectorSummary[]>([])
  const selectedAgents = ref<ConnectorId[]>([])
  const detectingAgents = ref(false)
  const detectionError = ref('')
  const installing = ref(false)
  const installOutcomes = ref<ConnectorOperationOutcome[]>([])
  const installFailures = ref<InstallFailure[]>([])
  const installOutcome = computed(() => installOutcomes.value[0] ?? null)
  const installHint = computed(() => installFailures.value[0]?.hint ?? null)
  const installError = computed(() =>
    installFailures.value.map((failure) => `${connectorLabel(failure.agent, connectors.value)}: ${failure.error}`).join('；')
  )

  const demoPrompt = computed(() => {
    // 显式读取 locale，让复制内容和页面语言保持同一个响应式来源。
    i18n.global.locale.value
    return i18n.global.t('onboarding.demoPrompt')
  })

  function isAgentInstalled(agent: ConnectorId) {
    return connectors.value.find(item => item.descriptor.id === agent)?.state.detected === true
  }

  function isAgentSelected(agent: ConnectorId) {
    return selectedAgents.value.includes(agent)
  }

  function agentAvailabilityLabel(agent: ConnectorId) {
    const status = connectors.value.find(item => item.descriptor.id === agent)?.state
    if (detectingAgents.value && !status) {
      return '检测中'
    }
    if (status?.detected === true) {
      return '已检测到'
    }
    if (detectionError.value && !status) {
      return '检测失败'
    }
    return '未检测到'
  }

  function toggleAgentSelection(agent: ConnectorId) {
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
    const started = performance.now()
    emitConnectorDiagnostic('list.started', 'info', { surface: 'onboarding' })
    detectingAgents.value = true
    detectionError.value = ''
    try {
      // 浏览器夹具在 onboarding 边界注入，避免污染设置页等共享 MCP API 消费者。
      const result = isOnboardingPreviewMode() ? previewConnectorSummaries() : await listAgentConnectors()
      connectors.value = result
      const installedAgents = result.filter(item => item.state.detected).map(item => item.descriptor.id)
      const preservedSelection = selectedAgents.value.filter(agent => installedAgents.includes(agent))
      selectedAgents.value = preservedSelection.length > 0 ? preservedSelection : installedAgents
      emitConnectorDiagnostic('list.succeeded', 'info', {
        surface: 'onboarding',
        detectedCount: installedAgents.length,
        detectedAgents: installedAgents,
        durationMs: Math.round(performance.now() - started),
      })
    } catch (error) {
      detectionError.value = error instanceof Error ? error.message : String(error)
      connectors.value = []
      selectedAgents.value = []
      emitConnectorDiagnostic('list.failed', 'error', {
        surface: 'onboarding',
        errorCode: 'agent_detection_failed',
        errorType: error instanceof Error ? error.name : typeof error,
        durationMs: Math.round(performance.now() - started),
      })
    } finally {
      detectingAgents.value = false
    }
  }

  async function installSelectedMcp() {
    // Store consumers outside the page receive the same serialization guard as disabled UI controls.
    if (installing.value) return
    installing.value = true
    installFailures.value = []
    const started = performance.now()
    try {
      const agents = selectedAgents.value.filter((agent) => isAgentInstalled(agent))
      emitConnectorDiagnostic('install.started', 'info', {
        surface: 'onboarding',
        selectedCount: agents.length,
        selectedAgents: agents,
      })
      let partialCount = 0
      for (const agent of agents) {
        const previous = installOutcomes.value.find(outcome => outcome.connector_id === agent)
        // A completed connector is retained across retry; partial/failed work is delegated to
        // the Registry with its prior outcome so only failed or needs-action capabilities rerun.
        if (previous && !['partial', 'failed', 'needs_action'].includes(previous.result)) continue
        try {
          const outcome = isOnboardingPreviewMode()
            ? previewConnectorOutcome(agent)
            : await installAgentConnector(agent, previous)
          const outcomeIndex = installOutcomes.value.findIndex(item => item.connector_id === agent)
          if (outcomeIndex >= 0) {
            // Replace in place so a retry cannot reorder cards relative to Registry order.
            installOutcomes.value[outcomeIndex] = outcome
          } else {
            installOutcomes.value.push(outcome)
          }
          const partial = outcome.result === 'partial' || outcome.result === 'needs_action'
          if (partial) partialCount += 1
          emitConnectorDiagnostic(partial ? 'install.partial' : 'install.succeeded', partial ? 'warn' : 'info', {
            surface: 'onboarding',
            agent,
            result: outcome.result,
            capabilityResults: outcome.integrations.map(item => `${item.capability}=${item.result}`),
          })
        } catch (error) {
          const hint = await getAgentConnectorManualInstructions(agent).catch(() => null)
          installFailures.value.push({
            agent,
            error: error instanceof Error ? error.message : String(error),
            hint,
          })
          emitConnectorDiagnostic('install.failed', 'error', {
            surface: 'onboarding',
            agent,
            errorCode: 'agent_install_failed',
            errorType: error instanceof Error ? error.name : typeof error,
            hintAvailable: hint !== null,
          })
        }
      }
      emitConnectorDiagnostic(
        'install.completed',
        installFailures.value.length > 0 || partialCount > 0 ? 'warn' : 'info',
        {
          surface: 'onboarding', selectedCount: agents.length,
          successCount: installOutcomes.value.filter(outcome =>
            agents.includes(outcome.connector_id)
            && !['partial', 'failed', 'needs_action'].includes(outcome.result),
          ).length,
          partialCount,
          failureCount: installFailures.value.length,
          durationMs: Math.round(performance.now() - started),
        },
      )
      if (!isOnboardingPreviewMode()) {
        // Status is refreshed after mutations so the page never treats the write result as a
        // runtime connection proof.
        try {
          connectors.value = await listAgentConnectors()
        } catch (error) {
          detectionError.value = error instanceof Error ? error.message : String(error)
          emitConnectorDiagnostic('status.refresh.failed', 'error', {
            surface: 'onboarding',
            errorCode: 'connector_status_refresh_failed',
            errorType: error instanceof Error ? error.name : typeof error,
          })
        }
      }
    } finally {
      installing.value = false
    }
  }

  return {
    selectedAgents,
    connectors,
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

function connectorLabel(id: ConnectorId, summaries: AgentConnectorSummary[]): string {
  return summaries.find(summary => summary.descriptor.id === id)?.descriptor.display_name
    ?? id.replace(/[-_]/g, ' ')
}
