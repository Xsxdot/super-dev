// remote store 集中管理远程监听域的 Host、LogSource 与 Tunnel 内存状态。
//
// 职责：
//   - 拉取并缓存 Host / LogSource 列表
//   - 对 LogSource 按其关联 Host 的 tag 计算分组
//   - 缓存隧道状态，供设置页和远程日志面板展示
//
// 边界：
//   - 不直接发起 WebSocket 连接，实时日志由 remoteLog store 负责
//   - 不渲染任何 UI，组件只通过 actions 和 getters 访问
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  api,
  type Host,
  type HostCreatePayload,
  type HostUpdatePayload,
  type LogSource,
  type LogSourceCreatePayload,
  type LogSourceUpdatePayload,
  type TunnelStatus,
} from '@/api/agent'

export interface Group {
  key: string
  hostIds: string[]
}

export const useRemoteStore = defineStore('remote', () => {
  const hosts = ref<Host[]>([])
  const logSources = ref<LogSource[]>([])
  const tunnels = ref<Map<string, TunnelStatus>>(new Map())

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

  async function deleteHost(id: string) {
    await api.deleteHost(id)
    hosts.value = hosts.value.filter(host => host.id !== id)
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
    const byTag = new Map<string, string[]>([['all', allHostIds]])

    for (const hostId of allHostIds) {
      const host = hostMap.get(hostId)
      if (!host) continue
      for (const tag of host.tags) {
        const taggedHostIds = byTag.get(tag) ?? []
        taggedHostIds.push(hostId)
        byTag.set(tag, taggedHostIds)
      }
    }

    return Array.from(byTag.entries()).map(([key, hostIds]) => ({ key, hostIds }))
  }

  async function loadTunnels() {
    const list = await api.listTunnels()
    tunnels.value = new Map(list.map(status => [status.host_id, status]))
  }

  function applyTunnelUpdate(status: TunnelStatus) {
    const next = new Map(tunnels.value)
    const previous = next.get(status.host_id)
    next.set(status.host_id, { ...previous, ...status })
    tunnels.value = next
  }

  function tunnelOf(hostId: string): TunnelStatus | undefined {
    return tunnels.value.get(hostId)
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
    tunnels,
    tagsAcrossHosts,
    loadHosts,
    createHost,
    updateHost,
    deleteHost,
    hostById,
    loadLogSources,
    createLogSource,
    updateLogSource,
    deleteLogSource,
    logSourceById,
    groupsOf,
    loadTunnels,
    applyTunnelUpdate,
    tunnelOf,
  }
})
