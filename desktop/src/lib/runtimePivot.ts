// runtimePivot 提供运行态实例的两级维度透视(pivot)分组。
//
// 职责：
//   - 把扁平实例数组按「一级维度 + 二级维度」分成两层嵌套结构
//   - 维度取值通过 accessor 表统一映射, 新增维度只改一处
//
// 边界：
//   - 不拉取数据、不排序服务/环境的业务序(调用方在投影时已排好序)
//   - 仅按实例数组的现有顺序稳定分组, 保持入参相对顺序
import type { RuntimeInstanceStatus } from '@/api/agent'

export type Dimension = 'service' | 'env' | 'node'

export interface PivotSubGroup {
  key: string
  label: string
  instances: RuntimeInstanceStatus[]
}

export interface PivotGroup {
  key: string
  label: string
  children: PivotSubGroup[]
}

// dimensionKey 返回某实例在指定维度下的分组 key 与展示 label。
// 服务/节点用 id 作 key、name 作 label; 环境 name 同时作 key 与 label。
function dimensionKey(instance: RuntimeInstanceStatus, dim: Dimension): { key: string; label: string } {
  switch (dim) {
    case 'service':
      return { key: instance.service_id, label: instance.service_name }
    case 'node':
      return { key: instance.node_id, label: instance.node_name }
    case 'env':
      return { key: instance.env_name, label: instance.env_name }
  }
}

// pivotInstances 把实例数组按一级、二级维度分成两层。
//
// 参数：
//   - instances: 已按业务序排好的扁平实例数组
//   - primary: 一级分组维度
//   - secondary: 二级分组维度
//
// 返回：两层嵌套 PivotGroup[], 保持入参的相对顺序(稳定分组)。
export function pivotInstances(
  instances: RuntimeInstanceStatus[],
  primary: Dimension,
  secondary: Dimension,
): PivotGroup[] {
  const groups: PivotGroup[] = []
  const groupIndex = new Map<string, PivotGroup>()
  const subIndex = new Map<string, PivotSubGroup>()

  for (const instance of instances) {
    const p = dimensionKey(instance, primary)
    let group = groupIndex.get(p.key)
    if (!group) {
      group = { key: p.key, label: p.label, children: [] }
      groupIndex.set(p.key, group)
      groups.push(group)
    }

    const s = dimensionKey(instance, secondary)
    const subKey = `${p.key}\u0000${s.key}`
    let sub = subIndex.get(subKey)
    if (!sub) {
      sub = { key: s.key, label: s.label, instances: [] }
      subIndex.set(subKey, sub)
      group.children.push(sub)
    }
    sub.instances.push(instance)
  }

  return groups
}
