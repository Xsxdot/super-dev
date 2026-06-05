import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import {
  usePanelStore,
  projectIdFromPanelSource,
  isSamePanelSource,
  type PanelSplitNode,
} from '../panel'

describe('isSamePanelSource', () => {
  it('deployment 来源按 deploymentId 匹配：相同时返回 true', () => {
    expect(isSamePanelSource(
      { type: 'deployment', deploymentId: 'd1' },
      { type: 'deployment', deploymentId: 'd1' },
    )).toBe(true)
  })

  it('deployment 来源按 deploymentId 匹配：不同时返回 false', () => {
    expect(isSamePanelSource(
      { type: 'deployment', deploymentId: 'd1' },
      { type: 'deployment', deploymentId: 'd2' },
    )).toBe(false)
  })
})

describe('projectIdFromPanelSource', () => {
  it('deployment 单源下恒返回 null（项目由 agentStore 反查）', () => {
    expect(projectIdFromPanelSource(
      { type: 'deployment', deploymentId: 'dep-1' },
    )).toBeNull()
  })

  it('空来源返回 null', () => {
    expect(projectIdFromPanelSource(null)).toBeNull()
  })
})

describe('panelStore', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('初始状态：单个空叶子节点', () => {
    const store = usePanelStore()
    expect(store.root.type).toBe('leaf')
    expect(store.allLeaves).toHaveLength(1)
  })

  it('splitLeaf：叶子节点变为 split，包含 2 个叶子', () => {
    const store = usePanelStore()
    const leafId = store.root.id
    store.splitLeaf(leafId, 'h', 'svc-1', 'proj-1', 'second')
    expect(store.root.type).toBe('split')
    expect(store.allLeaves).toHaveLength(2)
  })

  it('removeLeaf：删除一个叶子后回到单面板', () => {
    const store = usePanelStore()
    const leafId = store.root.id
    store.splitLeaf(leafId, 'h', 'svc-1', 'proj-1', 'second')
    const [leaf1, leaf2] = store.allLeaves
    store.removeLeaf(leaf2.id)
    expect(store.allLeaves).toHaveLength(1)
    expect(store.allLeaves[0].id).toBe(leaf1.id)
  })

  it('replaceScope：更新叶子的 serviceId', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.replaceScope(leafId, 'svc-abc', 'proj-xyz')
    expect(store.allLeaves[0].serviceId).toBe('svc-abc')
  })

  it('splitLeafWithSource：将 deployment 面板拆分为两栏并获得焦点', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.replaceSource(leafId, { type: 'deployment', deploymentId: 'dep-A' })

    store.splitLeafWithSource(
      leafId,
      'h',
      { type: 'deployment', deploymentId: 'dep-B' },
      'first',
    )

    expect(store.root.type).toBe('split')
    expect(store.allLeaves.map(leaf => leaf.source)).toEqual([
      { type: 'deployment', deploymentId: 'dep-B' },
      { type: 'deployment', deploymentId: 'dep-A' },
    ])
    expect(store.focusedPanelId).toBe(store.allLeaves[0].id)
  })

  it('removeLeaf：关闭焦点面板后保留兄弟来源且不允许最后一个面板消失', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.replaceSource(leafId, { type: 'deployment', deploymentId: 'dep-A' })
    store.splitLeafWithSource(
      leafId,
      'h',
      { type: 'deployment', deploymentId: 'dep-B' },
      'second',
    )
    const secondLeaf = store.allLeaves[1] ?? { id: 'missing-panel' }
    store.setFocus(secondLeaf.id)

    store.removeLeaf(secondLeaf.id)

    expect(store.allLeaves).toHaveLength(1)
    expect(store.allLeaves[0].source).toEqual({ type: 'deployment', deploymentId: 'dep-A' })
    expect(store.focusedPanelId).toBe(store.allLeaves[0].id)

    store.removeLeaf(store.allLeaves[0].id)

    expect(store.allLeaves).toHaveLength(1)
    expect(store.allLeaves[0].source).toEqual({ type: 'deployment', deploymentId: 'dep-A' })
  })

  it('balanceSplits：按叶子数量重算 split 比例，让已有面板均分空间', () => {
    const store = usePanelStore()
    const [first] = store.allLeaves
    store.setRoot({
      type: 'split',
      id: 'root-split',
      axis: 'h',
      ratio: 0.9,
      first: { ...first, id: 'leaf-a', serviceId: 'dep-A', projectId: null, source: { type: 'deployment', deploymentId: 'dep-A' } },
      second: {
        type: 'split',
        id: 'nested-split',
        axis: 'v',
        ratio: 0.8,
        first: { ...first, id: 'leaf-b', serviceId: 'dep-B', projectId: null, source: { type: 'deployment', deploymentId: 'dep-B' } },
        second: { ...first, id: 'leaf-c', serviceId: 'dep-C', projectId: null, source: { type: 'deployment', deploymentId: 'dep-C' } },
      },
    })

    store.balanceSplits()

    const root = store.root as PanelSplitNode
    expect(root.ratio).toBeCloseTo(1 / 3)
    expect((root.second as PanelSplitNode).ratio).toBeCloseTo(1 / 2)
  })

  it('arrangeLeavesInColumns：保留现有 deployment 顺序并重排为横向列', () => {
    const store = usePanelStore()
    const [first] = store.allLeaves
    store.setRoot({
      type: 'split',
      id: 'root-split',
      axis: 'v',
      ratio: 0.5,
      first: {
        type: 'split',
        id: 'nested-split',
        axis: 'v',
        ratio: 0.5,
        first: { ...first, id: 'leaf-a', serviceId: 'dep-A', projectId: null, source: { type: 'deployment', deploymentId: 'dep-A' } },
        second: { ...first, id: 'leaf-b', serviceId: 'dep-B', projectId: null, source: { type: 'deployment', deploymentId: 'dep-B' } },
      },
      second: { ...first, id: 'leaf-c', serviceId: 'dep-C', projectId: null, source: { type: 'deployment', deploymentId: 'dep-C' } },
    }, 'leaf-b')

    store.arrangeLeavesInColumns()

    const root = store.root as PanelSplitNode
    expect(root.axis).toBe('h')
    expect(root.ratio).toBeCloseTo(2 / 3)
    expect((root.first as PanelSplitNode).axis).toBe('h')
    expect(store.allLeaves.map(leaf => leaf.source)).toEqual([
      { type: 'deployment', deploymentId: 'dep-A' },
      { type: 'deployment', deploymentId: 'dep-B' },
      { type: 'deployment', deploymentId: 'dep-C' },
    ])
    expect(store.focusedPanelId).toBe('leaf-b')
  })
})
