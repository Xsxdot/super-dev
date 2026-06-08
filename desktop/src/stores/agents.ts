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
  type AgentConfigUpdatePayload,
  type AgentCreatePayload,
  type AgentInstallPayload,
  type AgentDTO,
  type AgentInstallCommandPayload,
  type AgentProvisionPayload,
  type AgentTransportUpdatePayload,
  type AgentUpdateBinaryResponse,
  type AgentUpdateTargetResponse,
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

  async function createAgent(payload: AgentCreatePayload) {
    const created = await api.createAgent(payload)
    upsert(created)
    return created
  }

  async function updateAgentTransport(hostId: string, payload: AgentTransportUpdatePayload) {
    const updated = await api.updateAgentTransport(hostId, payload)
    upsert(updated)
    return updated
  }

  async function updateAgent(hostId: string, payload: AgentTransportUpdatePayload) {
    return updateAgentTransport(hostId, payload)
  }

  async function updateAgentConfig(hostId: string, payload: AgentConfigUpdatePayload) {
    const updated = await api.updateAgentConfig(hostId, payload)
    upsert(updated)
    return updated
  }

  async function checkAgent(hostId: string) {
    const checked = await api.checkAgent(hostId)
    upsert(checked)
    return checked
  }

  async function installAgent(hostId: string, payload: AgentInstallPayload) {
    return api.installAgent(hostId, payload)
  }

  async function restartAgent(hostId: string) {
    return api.restartAgent(hostId)
  }

  async function getAgentUpdateTarget(): Promise<AgentUpdateTargetResponse> {
    return api.getAgentUpdateTarget()
  }

  async function updateAgentBinary(hostId: string): Promise<AgentUpdateBinaryResponse> {
    return api.updateAgentBinary(hostId)
  }

  async function deleteAgent(hostId: string) {
    await api.deleteAgent(hostId)
    agents.value = agents.value.filter(agent => agent.host_id !== hostId)
  }

  async function generateInstallCommand(hostId: string, payload: AgentInstallCommandPayload) {
    return api.generateAgentInstallCommand(hostId, payload)
  }

  async function testTransport(hostId: string, index: number) {
    return api.testAgentTransport(hostId, { index })
  }

  async function provisionAgent(hostId: string, payload: AgentProvisionPayload) {
    const result = await api.provisionAgent(hostId, payload)
    await loadAgents()
    return result
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
    createAgent,
    updateAgent,
    updateAgentTransport,
    updateAgentConfig,
    checkAgent,
    installAgent,
    restartAgent,
    getAgentUpdateTarget,
    updateAgentBinary,
    deleteAgent,
    generateInstallCommand,
    testTransport,
    provisionAgent,
    upsert,
    agentOf,
  }
})
