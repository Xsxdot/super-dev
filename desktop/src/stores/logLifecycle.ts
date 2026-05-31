// logLifecycleStore 维护当前前端会话内的 deployment 生命周期分割 marker。
//
// 职责：
//   - 记录 start/stop/restart 操作成功后的显示 marker
//   - 按 deploymentId 提供 marker 列表给日志面板渲染
//
// 边界：
//   - 不持久化 marker，刷新或重启应用后允许丢失
//   - 不写入真实日志流，不参与日志过滤、导出或搜索
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type LogLifecycleKind = 'start' | 'stop' | 'restart'

export interface LogLifecycleMarker {
  id: string
  deploymentId: string
  kind: LogLifecycleKind
  createdAt: string
}

const MAX_MARKERS_PER_DEPLOYMENT = 200

export const useLogLifecycleStore = defineStore('logLifecycle', () => {
  const markersByDeployment = ref<Record<string, LogLifecycleMarker[]>>({})

  function recordMarker(deploymentId: string, kind: LogLifecycleKind, at = new Date()) {
    const markers = markersByDeployment.value[deploymentId] ?? []
    markers.push({
      id: crypto.randomUUID(),
      deploymentId,
      kind,
      createdAt: at.toISOString(),
    })
    markersByDeployment.value[deploymentId] = markers.slice(-MAX_MARKERS_PER_DEPLOYMENT)
  }

  function getMarkers(deploymentId: string): LogLifecycleMarker[] {
    return markersByDeployment.value[deploymentId] ?? []
  }

  return {
    markersByDeployment,
    recordMarker,
    getMarkers,
  }
})
