// remote store 集中管理远程监听域的 Host、LogSource 与 managed deployment 内存状态。
//
// 职责：
//   - 拉取并缓存 Host / LogSource 列表
//   - 对 LogSource 按其关联 Host 的 tag 计算分组
//   - 缓存远端 managed deployment 状态，供侧边栏、底部栏、日志面板共享
//
// 边界：
//   - 不直接发起 WebSocket 连接，实时日志由 log store 负责
//   - 不管理 Agent 连接配置或安装动作，统一由 agents store 负责
//   - 不渲染任何 UI，组件只通过 actions 和 getters 访问
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  api,
  type Host,
  type HostCreatePayload,
  type HostManagedDeploymentStatus,
  type HostUpdatePayload,
  type LogSource,
  type LogSourceCreatePayload,
  type LogSourceUpdatePayload,
} from '@/api/agent'
import { useAgentStore } from '@/stores/agent'

export interface Group {
  key: string
  hostIds: string[]
}

export interface RemoteServiceGroup {
  serviceId: string
  serviceName: string
  logSourceIds: string[]
  groups: Group[]
}

export const useRemoteStore = defineStore('remote', () => {
  const hosts = ref<Host[]>([])
  const logSources = ref<LogSource[]>([])
  const managedStatuses = ref<Map<string, HostManagedDeploymentStatus>>(new Map())

  async function loadHosts() {
    hosts.value = await api.listHosts()
  }

  async function createHost(payload: HostCreatePayload) {
    const created = await api.createHost(payload)
    hosts.value.push(created)
    return created
  }

  async function updateHost(id: string, payload: HostUpdatePayload) {
    const updated = await api.updateHost(id, payload)
    const idx = hosts.value.findIndex(host => host.id === id)
    if (idx >= 0) hosts.value[idx] = updated
    return updated
  }

  // 只读采集，不改动 store 内的 hosts 状态：信任决策由用户确认后经 create/updateHost 落库。
  async function scanHostKey(payload: { ssh_host: string; ssh_port: number }) {
    return api.scanHostKey(payload)
  }

  async function deleteHost(id: string) {
    await api.deleteHost(id)
    hosts.value = hosts.value.filter(host => host.id !== id)
    const nextStatuses = new Map(managedStatuses.value)
    nextStatuses.delete(id)
    managedStatuses.value = nextStatuses
  }

  async function getHostManagedDeploymentStatus(id: string): Promise<HostManagedDeploymentStatus> {
    const status = await api.getHostManagedDeploymentStatus(id)
    applyManagedStatus(status)
    return status
  }

  async function refreshManagedStatuses(ids: string[]): Promise<HostManagedDeploymentStatus[]> {
    const uniqueIds = [...new Set(ids.filter(id => id.trim().length > 0))]
    return Promise.all(uniqueIds.map(id => getHostManagedDeploymentStatus(id)))
  }

  function applyManagedStatus(status: HostManagedDeploymentStatus) {
    const next = new Map(managedStatuses.value)
    next.set(status.host_id, status)
    managedStatuses.value = next
  }

  function managedStatusOf(hostId: string): HostManagedDeploymentStatus | undefined {
    return managedStatuses.value.get(hostId)
  }

  function hostById(id: string): Host | undefined {
    return hosts.value.find(host => host.id === id)
  }

  async function loadLogSources() {
    logSources.value = await api.listLogSources()
  }

  async function createLogSource(payload: LogSourceCreatePayload) {
    const created = await api.createLogSource(payload)
    logSources.value.push(created)
    return created
  }

  async function updateLogSource(id: string, payload: LogSourceUpdatePayload) {
    const updated = await api.updateLogSource(id, payload)
    const idx = logSources.value.findIndex(logSource => logSource.id === id)
    if (idx >= 0) logSources.value[idx] = updated
    return updated
  }

  async function deleteLogSource(id: string) {
    await api.deleteLogSource(id)
    logSources.value = logSources.value.filter(logSource => logSource.id !== id)
  }

  function logSourceById(id: string): LogSource | undefined {
    return logSources.value.find(logSource => logSource.id === id)
  }

  function groupsOf(logSourceId: string): Group[] {
    const logSource = logSourceById(logSourceId)
    if (!logSource) return []

    const hostMap = new Map(hosts.value.map(host => [host.id, host]))
    const allHostIds = logSource.host_ids.filter(id => hostMap.has(id))

    // 按 LogSource 自身的 tags 分组；每个 tag 分组包含全部 host_ids。
    // all 分组始终存在，tag 分组按字母排序排在 all 之后。
    const groups: Group[] = [{ key: 'all', hostIds: allHostIds }]
    const sortedTags = [...(logSource.tags ?? [])].sort((a, b) => a.localeCompare(b))
    for (const tag of sortedTags) {
      groups.push({ key: tag, hostIds: allHostIds })
    }

    return groups
  }

  // remoteServiceGroupsOf 返回指定项目下按服务聚合的远程监听分组。
  // 分组规则：
  //   - all: 所有参与聚合的 LogSource 的 HostIDs 合集
  //   - tag 分组: 只含打了该 tag 的 LogSource 对应的 HostIDs
  function remoteServiceGroupsOf(projectId: string): RemoteServiceGroup[] {
    const bound = logSources.value.filter(ls => ls.project_id === projectId && ls.service_id)
    if (bound.length === 0) return []

    const byService = new Map<string, LogSource[]>()
    for (const ls of bound) {
      const key = ls.service_id!
      if (!byService.has(key)) byService.set(key, [])
      byService.get(key)!.push(ls)
    }

    const hostMap = new Map(hosts.value.map(h => [h.id, h]))

    return Array.from(byService.entries()).map(([serviceId, sources]) => {
      const allHostIds = [...new Set(sources.flatMap(ls => ls.host_ids.filter(id => hostMap.has(id))))]

      const tagToHosts = new Map<string, string[]>()
      for (const ls of sources) {
        const validHosts = ls.host_ids.filter(id => hostMap.has(id))
        for (const tag of ls.tags ?? []) {
          if (!tagToHosts.has(tag)) tagToHosts.set(tag, [])
          const existing = tagToHosts.get(tag)!
          for (const h of validHosts) {
            if (!existing.includes(h)) existing.push(h)
          }
        }
      }
      const sortedTags = [...tagToHosts.keys()].sort((a, b) => a.localeCompare(b))

      const groups: Group[] = [{ key: 'all', hostIds: allHostIds }]
      for (const tag of sortedTags) {
        groups.push({ key: tag, hostIds: tagToHosts.get(tag)! })
      }

      const agentStore = useAgentStore()
      const project = agentStore.projectById(projectId)
      const svc = project?.services.find(s => s.id === serviceId)
      const serviceName = svc?.name ?? serviceId.slice(0, 16)

      return {
        serviceId,
        serviceName,
        logSourceIds: sources.map(ls => ls.id),
        groups,
      }
    })
  }

  const tagsAcrossHosts = computed(() => {
    const tags = new Set<string>()
    for (const host of hosts.value) {
      for (const tag of host.tags) tags.add(tag)
    }
    return Array.from(tags).sort()
  })

  return {
    hosts,
    logSources,
    managedStatuses,
    tagsAcrossHosts,
    loadHosts,
    createHost,
    updateHost,
    scanHostKey,
    deleteHost,
    getHostManagedDeploymentStatus,
    refreshManagedStatuses,
    applyManagedStatus,
    managedStatusOf,
    hostById,
    loadLogSources,
    createLogSource,
    updateLogSource,
    deleteLogSource,
    logSourceById,
    groupsOf,
    remoteServiceGroupsOf,
  }
})
