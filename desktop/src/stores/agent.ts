// agentStore 负责轮询 agent 获取项目和服务列表，维护连接状态。

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Deployment, type Project, type Service } from '@/api/agent'

export const useAgentStore = defineStore('agent', () => {
  const projects = ref<Project[]>([])
  const connected = ref(false)
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function fetchProjects() {
    try {
      projects.value = await api.listProjects()
      connected.value = true
    } catch {
      connected.value = false
    }
  }

  async function refreshServices() {
    if (!connected.value) return
    try {
      const services = await api.listServices()
      // 用最新状态更新 projects 里的 services，保留其他字段
      for (const project of projects.value) {
        const updated = services.filter(s => s.project_id === project.id)
        if (updated.length > 0) {
          project.services = updated
        }
      }
    } catch {
      connected.value = false
    }
  }

  function startPolling() {
    void connectWithRetry()
    pollTimer = setInterval(() => {
      if (!connected.value) void fetchProjects()
      else void refreshServices()
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
    await api.startDeployment(id)
  }

  async function stopDeployment(id: string) {
    await api.stopDeployment(id)
  }

  async function restartDeployment(id: string) {
    await api.restartDeployment(id)
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
    await api.startEnvSelected(projectId, envName)
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
