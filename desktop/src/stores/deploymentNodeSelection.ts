/**
 * deploymentNodeSelectionStore 保存日志面板的节点筛选状态。
 *
 * 职责：
 *   - 按 deploymentId 记录用户勾选的 host_id 列表
 *   - 新节点首次出现时默认选中，避免新增主机后没有日志
 *
 * 边界：
 *   - 不读取 Host、Deployment 或日志数据
 *   - 不执行启停等运行态操作，节点勾选只影响日志展示范围
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

function unique(values: string[]): string[] {
  return [...new Set(values.filter(value => value.trim().length > 0))]
}

export const useDeploymentNodeSelectionStore = defineStore('deploymentNodeSelection', () => {
  const knownByDeployment = ref<Record<string, string[]>>({})
  const selectedByDeployment = ref<Record<string, string[]>>({})

  function ensureDeploymentNodes(deploymentId: string, hostIds: string[]) {
    const nextKnown = unique(hostIds)
    const previousKnown = new Set(knownByDeployment.value[deploymentId] ?? [])
    const previousSelected = new Set(selectedByDeployment.value[deploymentId] ?? [])
    const firstSeen = previousKnown.size === 0

    knownByDeployment.value = {
      ...knownByDeployment.value,
      [deploymentId]: nextKnown,
    }
    selectedByDeployment.value = {
      ...selectedByDeployment.value,
      [deploymentId]: firstSeen
        ? nextKnown
        : nextKnown.filter(hostId => previousSelected.has(hostId) || !previousKnown.has(hostId)),
    }
  }

  function selectedHostIds(deploymentId: string): string[] {
    const known = new Set(knownByDeployment.value[deploymentId] ?? [])
    return (selectedByDeployment.value[deploymentId] ?? []).filter(hostId => known.has(hostId))
  }

  function toggleNode(deploymentId: string, hostId: string) {
    const known = new Set(knownByDeployment.value[deploymentId] ?? [hostId])
    if (!known.has(hostId)) known.add(hostId)
    const selected = new Set(selectedByDeployment.value[deploymentId] ?? Array.from(known))
    if (selected.has(hostId)) selected.delete(hostId)
    else selected.add(hostId)
    knownByDeployment.value = {
      ...knownByDeployment.value,
      [deploymentId]: Array.from(known),
    }
    selectedByDeployment.value = {
      ...selectedByDeployment.value,
      [deploymentId]: Array.from(selected).filter(id => known.has(id)),
    }
  }

  function setAll(deploymentId: string, hostIds: string[], checked: boolean) {
    const nextKnown = unique(hostIds)
    knownByDeployment.value = {
      ...knownByDeployment.value,
      [deploymentId]: nextKnown,
    }
    selectedByDeployment.value = {
      ...selectedByDeployment.value,
      [deploymentId]: checked ? nextKnown : [],
    }
  }

  function isNodeSelected(deploymentId: string, hostId: string): boolean {
    return selectedHostIds(deploymentId).includes(hostId)
  }

  return {
    knownByDeployment,
    selectedByDeployment,
    ensureDeploymentNodes,
    selectedHostIds,
    toggleNode,
    setAll,
    isNodeSelected,
  }
})
