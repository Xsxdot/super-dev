/**
 * nodeStore 管理 NodeRegistry 的前端内存快照。
 *
 * 职责：
 *   - 启动时从 /api/nodes 获取初始快照
 *   - 通过 /ws/nodes 订阅后端 NodeRegistry 全量快照
 *   - 将 NodeStatus 转成现有 HostManagedDeploymentStatus 形状，供旧 UI 过渡复用
 *
 * 边界：
 *   - 不管理日志 WebSocket
 *   - 不主动打开或关闭 SSH 隧道
 *   - 不渲染任何 UI
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api, nodesWsUrl, type HostManagedDeploymentStatus, type NodeStatus } from '@/api/agent'

export const useNodeStore = defineStore('node', () => {
  const nodes = ref<Map<string, NodeStatus>>(new Map())
  const connected = ref(false)
  const error = ref<string | null>(null)
  let ws: WebSocket | null = null
  let starting: Promise<void> | null = null
  let activeConsumers = 0

  const nodesList = computed(() =>
    [...nodes.value.values()].sort((a, b) => {
      const left = a.name || a.host_id
      const right = b.name || b.host_id
      return left.localeCompare(right) || a.host_id.localeCompare(b.host_id)
    }),
  )

  const managedStatuses = computed<Map<string, HostManagedDeploymentStatus>>(() => {
    const out = new Map<string, HostManagedDeploymentStatus>()
    for (const node of nodes.value.values()) {
      out.set(node.host_id, nodeToManagedStatus(node))
    }
    return out
  })

  function applySnapshot(snapshot: NodeStatus[]) {
    const next = new Map(nodes.value)
    for (const node of snapshot) {
      if (!node.host_id) continue
      next.set(node.host_id, node)
    }
    nodes.value = next
  }

  async function loadSnapshot() {
    const snapshot = await api.listNodes()
    nodes.value = new Map(snapshot.filter(node => node.host_id).map(node => [node.host_id, node]))
  }

  async function start() {
    activeConsumers += 1
    if (ws) return
    if (starting) return starting
    starting = startInner().finally(() => {
      starting = null
    })
    return starting
  }

  async function startInner() {
    try {
      await loadSnapshot()
      error.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load node snapshot'
    }
    if (activeConsumers <= 0 || ws) return
    ws = new WebSocket(nodesWsUrl())
    ws.onopen = () => {
      connected.value = true
      error.value = null
    }
    ws.onmessage = event => {
      try {
        applySnapshot(JSON.parse(event.data) as NodeStatus[])
      } catch {
        // 忽略损坏帧，避免单条异常影响整条状态线。
      }
    }
    ws.onerror = () => {
      error.value = 'Node status stream error'
    }
    ws.onclose = () => {
      connected.value = false
      ws = null
    }
  }

  function stop() {
    if (activeConsumers > 0) activeConsumers -= 1
    if (activeConsumers > 0) return
    ws?.close()
    ws = null
    connected.value = false
  }

  function nodeOf(hostId: string): NodeStatus | undefined {
    return nodes.value.get(hostId)
  }

  return {
    nodes,
    nodesList,
    managedStatuses,
    connected,
    error,
    start,
    stop,
    applySnapshot,
    nodeOf,
  }
})

function nodeToManagedStatus(node: NodeStatus): HostManagedDeploymentStatus {
  const deployments = nodeDeployments(node)
  return {
    host_id: node.host_id,
    host_name: node.name,
    desired_deployment_count: node.managed?.deployment_count ?? deployments.length,
    desired_collector_count: node.managed?.collector_count ?? 0,
    tunnel_connected: node.reachable,
    remote: node.reachable ? node.managed : undefined,
    error: node.error || (node.reachable ? undefined : 'node unreachable'),
  }
}

function nodeDeployments(node: NodeStatus): NodeStatus['deployments'] {
  // 兼容后端历史帧里的 deployments:null；这里按空数组派生，避免一个不可达节点拖垮共享 computed。
  return Array.isArray(node.deployments) ? node.deployments : []
}
