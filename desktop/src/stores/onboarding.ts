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
import {
  getMcpInstallHint,
  installMcp,
  type CodingAgent,
  type InstallHint,
  type InstallOutcome,
} from '@/api/mcpInstall'

export const codingAgents: Array<{ id: CodingAgent; label: string }> = [
  { id: 'claude-code', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
  { id: 'cursor', label: 'Cursor' },
]

export const useOnboardingStore = defineStore('onboarding', () => {
  const selectedAgent = ref<CodingAgent>('claude-code')
  const installing = ref(false)
  const installOutcome = ref<InstallOutcome | null>(null)
  const installHint = ref<InstallHint | null>(null)
  const installError = ref('')

  const demoPrompt = computed(() =>
    '帮我体验 SuperDev：请使用 SuperDev MCP 工具找到 superdev-sample 示例项目，查看它的服务状态和日志。' +
    '如果日志暂时为空，请对 sample-api 的 demo deployment 执行 restart_service。' +
    '这个重启动作会触发一次安全审批；请先告诉我去 SuperDev 的操作审批界面批准，批准后用 get_operation_approval 取得 approval token，再继续重启。' +
    '执行成功后再次读取日志，找出 WARN/ERROR 的含义，并用一两句话总结 SuperDev 如何在 AI 操作真实环境前把关。'
  )

  async function installSelectedMcp() {
    installing.value = true
    installError.value = ''
    installHint.value = null
    try {
      installOutcome.value = await installMcp(selectedAgent.value)
    } catch (error) {
      installError.value = error instanceof Error ? error.message : String(error)
      installHint.value = await getMcpInstallHint(selectedAgent.value).catch(() => null)
    } finally {
      installing.value = false
    }
  }

  return {
    selectedAgent,
    installing,
    installOutcome,
    installHint,
    installError,
    demoPrompt,
    installSelectedMcp,
  }
})
