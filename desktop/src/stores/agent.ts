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
    fetchProjects()
    pollTimer = setInterval(refreshServices, 2000)
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

  async function startSelected(projectId: string) {
    await api.startSelected(projectId)
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
    startSelected,
    serviceById,
    projectById,
  }
})
