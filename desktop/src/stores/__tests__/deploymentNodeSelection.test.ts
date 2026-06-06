/**
 * deploymentNodeSelectionStore 测试 deployment 节点勾选状态。
 *
 * 职责：
 *   - 验证节点首次出现时默认全选
 *   - 验证用户取消勾选后不会被刷新重置
 *
 * 边界：
 *   - 不参与日志过滤计算
 *   - 不读写本地存储
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useDeploymentNodeSelectionStore } from '@/stores/deploymentNodeSelection'

describe('deploymentNodeSelectionStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('首次见到 deployment 节点时默认全选', () => {
    const store = useDeploymentNodeSelectionStore()

    store.ensureDeploymentNodes('dep-api', ['h1', 'h2'])

    expect(store.selectedHostIds('dep-api')).toEqual(['h1', 'h2'])
  })

  it('刷新节点列表时保留已取消节点，同时默认选中新节点', () => {
    const store = useDeploymentNodeSelectionStore()

    store.ensureDeploymentNodes('dep-api', ['h1', 'h2'])
    store.toggleNode('dep-api', 'h2')
    store.ensureDeploymentNodes('dep-api', ['h1', 'h2', 'h3'])

    expect(store.selectedHostIds('dep-api')).toEqual(['h1', 'h3'])
  })
})
