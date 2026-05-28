// agentStore 负责轮询 agent 获取项目和服务列表，维护连接状态。

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Project, type Service } from '@/api/agent'

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

  async function deleteProject(id: string) {
    await api.deleteProject(id)
    projects.value = projects.value.filter(p => p.id !== id)
  }

  async function startService(id: string) {
    await api.startService(id)
  }

  async function stopService(id: string) {
    await api.stopService(id)
  }

  async function restartService(id: string) {
    await api.restartService(id)
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

  async function startSelected(projectId: string) {
    await api.startSelected(projectId)
  }

  async function updateSelected(projectId: string, names: string[]) {
    await api.putSelected(projectId, names)
    const project = projects.value.find(p => p.id === projectId)
    if (project) {
      project.selected_service_ids = names
    }
  }

  function isServiceSelectedForStart(projectId: string, serviceName: string): boolean {
    const project = projects.value.find(p => p.id === projectId)
    if (!project) return false
    const service = project.services.find(s => s.name === serviceName)
    if (service?.required) return true
    return project.selected_service_ids?.includes(serviceName) ?? false
  }

  const allServices = computed<Service[]>(() =>
    projects.value.flatMap(p => p.services)
  )

  function serviceById(id: string): Service | undefined {
    return allServices.value.find(s => s.id === id)
  }

  function projectById(id: string): Project | undefined {
    return projects.value.find(p => p.id === id)
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
    deleteProject,
    startService,
    stopService,
    restartService,
    startDeployment,
    stopDeployment,
    restartDeployment,
    putEnvSelected,
    startEnvSelected,
    isServiceEnvSelected,
    startSelected,
    updateSelected,
    isServiceSelectedForStart,
    serviceById,
    projectById,
    reloadProject,
  }
})
