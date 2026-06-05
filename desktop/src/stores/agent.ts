// agentStore 负责轮询 agent 获取项目和服务列表，维护连接状态。

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Deployment, type Project, type Service } from '@/api/agent'
import { useLogLifecycleStore } from '@/stores/logLifecycle'
import { useOperationApprovalStore } from '@/stores/operationApproval'

export const useAgentStore = defineStore('agent', () => {
  const projects = ref<Project[]>([])
  const connected = ref(false)
  const logLifecycleStore = useLogLifecycleStore()
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function fetchProjects() {
    try {
      projects.value = await api.listProjects()
      connected.value = true
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
    void connectWithRetry()
    pollTimer = setInterval(() => {
      if (!connected.value) void fetchProjects()
      else void refreshProjectSnapshot()
    }, 2000)
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

  async function startDeployment(id: string) {
    try {
      await api.startDeployment(id)
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

  async function restartDeployment(id: string) {
    try {
      await api.restartDeployment(id)
    } catch (err) {
      if (await captureApprovalRequired(err)) return
      throw err
    }
    logLifecycleStore.recordMarker(id, 'restart')
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
    allServices,
    startPolling,
    stopPolling,
    fetchProjects,
    addProject,
    probeProject,
    deleteProject,
    startDeployment,
    stopDeployment,
    restartDeployment,
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
