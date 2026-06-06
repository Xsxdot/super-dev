/**
 * agentsStore 管理远端 Host 上的一等 Agent 配置。
 *
 * 职责：
 *   - 拉取并缓存 /api/agents 返回的连接配置与运行态
 *   - 通过 Agent API 更新 transport，而不是复用 Host CRUD
 *   - 生成安装命令并保持命令结果不落入 Host 身份状态
 *
 * 边界：
 *   - 不管理项目、deployment 或日志生命周期
 *   - 不打开 NodeRegistry WebSocket
 *   - 不渲染设置页 UI
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import {
  api,
  type AgentDTO,
  type AgentInstallCommandPayload,
  type AgentUpdatePayload,
} from '@/api/agent'

export const useAgentsStore = defineStore('agents', () => {
  const agents = ref<AgentDTO[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  const agentsByHost = computed(() => new Map(agents.value.map(agent => [agent.host_id, agent])))

  async function loadAgents() {
    loading.value = true
    try {
      agents.value = await api.listAgents()
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load agents'
    } finally {
      loading.value = false
    }
  }

  async function updateAgent(hostId: string, payload: AgentUpdatePayload) {
    const updated = await api.updateAgent(hostId, payload)
    upsert(updated)
    return updated
  }

  async function checkAgent(hostId: string) {
    const checked = await api.checkAgent(hostId)
    upsert(checked)
    return checked
  }

  async function deleteAgent(hostId: string) {
    await api.deleteAgent(hostId)
    agents.value = agents.value.filter(agent => agent.host_id !== hostId)
  }

  async function generateInstallCommand(hostId: string, payload: AgentInstallCommandPayload) {
    return api.generateAgentInstallCommand(hostId, payload)
  }

  function upsert(agent: AgentDTO) {
    const idx = agents.value.findIndex(item => item.host_id === agent.host_id)
    if (idx >= 0) agents.value[idx] = agent
    else agents.value.push(agent)
  }

  function agentOf(hostId: string) {
    return agentsByHost.value.get(hostId)
  }

  return {
    agents,
    loading,
    error,
    agentsByHost,
    loadAgents,
    updateAgent,
    checkAgent,
    deleteAgent,
    generateInstallCommand,
    upsert,
    agentOf,
  }
})
