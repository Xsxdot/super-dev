// agentStore 负责轮询 agent 获取项目和服务列表，维护连接状态。

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { invoke } from '@tauri-apps/api/core'
import { api, type Deployment, type Project, type RuntimeIntent, type RuntimeSchema, type Service, type ServiceLanguage } from '@/api/agent'
import { useLogLifecycleStore } from '@/stores/logLifecycle'
import { useOperationApprovalStore } from '@/stores/operationApproval'

/**
 * AgentConnectionInfo 描述桌面端本次启动与 agent 的连接形态。
 *
 * - mode: 'sidecar' 表示桌面自己拉起了 agent 子进程；'attached' 表示挂接到
 *   已存在的服务化安装（或 headless）agent；'unknown' 表示两者都无法判定
 *   （通常是探测命令本身失败前的兜底态）。
 * - version / addr: attached 模式下用于在底栏展示对端 agent 的版本与地址；
 *   sidecar 模式通常没有独立版本号，可为空。
 */
export interface AgentConnectionInfo {
  mode: 'sidecar' | 'attached' | 'unknown'
  version?: string | null
  addr?: string
}

export const useAgentStore = defineStore('agent', () => {
  const projects = ref<Project[]>([])
  const connected = ref(false)
  const connectionInfo = ref<AgentConnectionInfo | null>(null)
  const languageRuntimeSchemas = ref<Partial<Record<ServiceLanguage, RuntimeSchema>>>({})
  const logLifecycleStore = useLogLifecycleStore()
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function fetchProjects() {
    const wasConnected = connected.value
    try {
      projects.value = await api.listProjects()
      connected.value = true
      // 断连→重连成功时刷新连接形态：attached 的服务化 agent 可能已被
      // launchd/systemd 重启（版本变化），甚至换成了别的进程占回端口——
      // 只在启动拉一次会让底栏一直展示过期的 attached 快照。
      if (!wasConnected) void fetchConnectionInfo()
    } catch {
      connected.value = false
    }
  }

  async function refreshProjectSnapshot() {
    if (!connected.value) return
    try {
      // MCP 等外部入口会修改项目集合；先拉完整项目快照，再叠加服务运行态。
      const [nextProjects, services] = await Promise.all([
        api.listProjects(),
        api.listServices(),
      ])
      const servicesByProject = new Map<string, Service[]>()
      for (const service of services) {
        const projectServices = servicesByProject.get(service.project_id) ?? []
        projectServices.push(service)
        servicesByProject.set(service.project_id, projectServices)
      }
      projects.value = nextProjects.map(project => {
        const updatedServices = servicesByProject.get(project.id)
        return updatedServices ? { ...project, services: updatedServices } : project
      })
      connected.value = true
    } catch {
      connected.value = false
    }
  }

  function startPolling() {
    void fetchConnectionInfo()
    void connectWithRetry()
    pollTimer = setInterval(() => {
      if (!connected.value) void fetchProjects()
      else void refreshProjectSnapshot()
    }, 2000)
  }

  /**
   * fetchConnectionInfo 只在启动时拉一次连接形态（sidecar/attached/unknown）。
   *
   * 之所以不放进轮询：attach 还是 sidecar 在桌面进程启动时就已定型
   * （由 Tauri 侧决定是否拉起子进程），生命周期内不会变化，重复轮询
   * 只会浪费一次 IPC 而拿不到新信息。
   */
  async function fetchConnectionInfo() {
    try {
      connectionInfo.value = await invoke<AgentConnectionInfo>('agent_connection_info')
    } catch {
      // 非 Tauri 环境（纯浏览器 dev / vitest）没有该命令，这是正常降级路径，
      // 不视为错误，因此不打日志，避免在预期状态下制造噪音。
      connectionInfo.value = null
    }
  }

  /** agent 由 Tauri 异步拉起，启动后需重试几次才能连上 */
  async function connectWithRetry(maxAttempts = 15, intervalMs = 400) {
    for (let i = 0; i < maxAttempts; i++) {
      await fetchProjects()
      if (connected.value) return
      await new Promise(r => setTimeout(r, intervalMs))
    }
  }

  function stopPolling() {
    if (pollTimer) clearInterval(pollTimer)
  }

  async function addProject(rootPath: string) {
    const project = await api.addProject(rootPath)
    projects.value.push(project)
    return project
  }

  async function probeProject(rootPath: string) {
    return api.probeProject(rootPath)
  }

  async function deleteProject(id: string) {
    await api.deleteProject(id)
    projects.value = projects.value.filter(p => p.id !== id)
  }

  async function startDeployment(id: string, intent?: RuntimeIntent) {
    try {
      await api.startDeployment(id, intent)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'start')
  }

  async function stopDeployment(id: string) {
    try {
      await api.stopDeployment(id)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'stop')
  }

  async function restartDeployment(id: string, intent?: RuntimeIntent) {
    try {
      await api.restartDeployment(id, intent)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'restart')
  }

  async function startDeploymentOnHost(id: string, hostId: string) {
    try {
      await api.startDeploymentOnHost(id, hostId)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'start')
  }

  async function stopDeploymentOnHost(id: string, hostId: string) {
    try {
      await api.stopDeploymentOnHost(id, hostId)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'stop')
  }

  async function restartDeploymentOnHost(id: string, hostId: string) {
    try {
      await api.restartDeploymentOnHost(id, hostId)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'restart')
  }

  async function describeLanguageRuntimeSchema(language: ServiceLanguage) {
    const cached = languageRuntimeSchemas.value[language]
    if (cached) return cached
    const schema = await api.describeLanguageRuntimeSchema(language)
    languageRuntimeSchemas.value = { ...languageRuntimeSchemas.value, [language]: schema }
    return schema
  }

  async function putEnvSelected(projectId: string, envName: string, names: string[]) {
    await api.putEnvSelected(projectId, envName, names)
    const project = projects.value.find(p => p.id === projectId)
    if (project) {
      if (!project.env_selected_service_ids) project.env_selected_service_ids = {}
      project.env_selected_service_ids[envName] = names
    }
  }

  async function startEnvSelected(projectId: string, envName: string) {
    try {
      await api.startEnvSelected(projectId, envName)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
  }

  async function captureApprovalRequired(err: unknown): Promise<boolean> {
    const operationApprovalStore = useOperationApprovalStore()
    return operationApprovalStore.captureApprovalRequired(err)
  }

  function isServiceEnvSelected(projectId: string, envName: string, serviceName: string): boolean {
    const project = projects.value.find(p => p.id === projectId)
    if (!project) return false
    const svc = project.services.find(s => s.name === serviceName)
    if (svc?.required) return true
    return project.env_selected_service_ids?.[envName]?.includes(serviceName) ?? false
  }

  const allServices = computed<Service[]>(() =>
    projects.value.flatMap(p => p.services)
  )

  function serviceById(id: string): Service | undefined {
    return allServices.value.find(s => s.id === id)
  }

  /**
   * deploymentById 在所有项目的所有 service 的 deployments 中查找指定 deployment。
   *
   * 参数：
   *   - id: deployment 唯一标识
   *
   * 返回：
   *   - 命中的 Deployment，未找到时返回 undefined
   */
  function deploymentById(id: string): Deployment | undefined {
    for (const service of allServices.value) {
      const dep = service.deployments?.find(d => d.id === id)
      if (dep) return dep
    }
    return undefined
  }

  /**
   * serviceForDeployment 反查 deployment 所属的 service 及其 env 名。
   *
   * 参数：
   *   - depId: deployment 唯一标识
   *
   * 返回：
   *   - { service, deployment, envName } 三元组；未找到时返回 undefined
   *
   * 注意：
   *   - 供面板标题显示「service 名 · env 名」使用
   */
  function serviceForDeployment(
    depId: string,
  ): { service: Service; deployment: Deployment; envName: string } | undefined {
    for (const service of allServices.value) {
      const dep = service.deployments?.find(d => d.id === depId)
      if (dep) return { service, deployment: dep, envName: dep.env_name }
    }
    return undefined
  }

  function projectById(id: string): Project | undefined {
    return projects.value.find(p => p.id === id)
  }

  /**
   * devEnvName 返回项目的开发环境名称。
   *
   * 优先取 is_dev 的环境；无标记时退回第一个环境；项目无环境时返回 'dev'。
   * 供无 env 选择 UI（托盘 Popover、设置页）默认作用于开发环境。
   */
  function devEnvName(projectId: string): string {
    const project = projectById(projectId)
    const envs = project?.environments ?? []
    const dev = envs.find(e => e.is_dev) ?? envs[0]
    return dev?.name ?? 'dev'
  }

  /**
   * deploymentForServiceInEnv 取出 service 在指定 env 下的 deployment。
   *
   * 参数：
   *   - serviceId: service 唯一标识
   *   - envName: 环境名
   *
   * 返回：
   *   - 命中的 Deployment，未找到时返回 undefined
   */
  function deploymentForServiceInEnv(serviceId: string, envName: string): Deployment | undefined {
    const svc = serviceById(serviceId)
    return svc?.deployments?.find(d => d.env_name === envName)
  }

  async function reloadProject(id: string) {
    const list = await api.listProjects()
    const updated = list.find(p => p.id === id)
    if (!updated) return
    const idx = projects.value.findIndex(p => p.id === id)
    if (idx !== -1) {
      projects.value[idx] = updated
    } else {
      projects.value.push(updated)
    }
  }

  return {
    projects,
    connected,
    connectionInfo,
    allServices,
    startPolling,
    stopPolling,
    fetchProjects,
    fetchConnectionInfo,
    addProject,
    probeProject,
    deleteProject,
    startDeployment,
    stopDeployment,
    restartDeployment,
    describeLanguageRuntimeSchema,
    startDeploymentOnHost,
    stopDeploymentOnHost,
    restartDeploymentOnHost,
    putEnvSelected,
    startEnvSelected,
    isServiceEnvSelected,
    serviceById,
    deploymentById,
    serviceForDeployment,
    projectById,
    devEnvName,
    deploymentForServiceInEnv,
    reloadProject,
  }
})
