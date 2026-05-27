import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { usePanelStore, projectIdFromPanelSource, isSamePanelSource } from '../panel'

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
  it('本地服务来源返回 projectId', () => {
    expect(projectIdFromPanelSource(
      { type: 'local-service', projectId: 'project-a', serviceId: 'svc-1' },
    )).toBe('project-a')
  })

  it('本地项目来源返回 projectId', () => {
    expect(projectIdFromPanelSource(
      { type: 'local-project', projectId: 'project-a' },
    )).toBe('project-a')
  })

  it('deployment 来源返回 null', () => {
    expect(projectIdFromPanelSource(
      { type: 'deployment', deploymentId: 'dep-1' },
    )).toBeNull()
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

  it('splitLeafWithSource：将本地面板拆分为两栏并获得焦点', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.replaceSource(leafId, {
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })

    store.splitLeafWithSource(
      leafId,
      'h',
      { type: 'local-project', projectId: 'project-B' },
      'first',
    )

    expect(store.root.type).toBe('split')
    expect(store.allLeaves.map(leaf => leaf.source)).toEqual([
      { type: 'local-project', projectId: 'project-B' },
      { type: 'local-service', projectId: 'project-A', serviceId: 'svc-api' },
    ])
    expect(store.focusedPanelId).toBe(store.allLeaves[0].id)
  })

  it('removeLeaf：关闭焦点面板后保留兄弟来源且不允许最后一个面板消失', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.replaceSource(leafId, {
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
    store.splitLeafWithSource(
      leafId,
      'h',
      { type: 'local-project', projectId: 'project-B' },
      'second',
    )
    const secondLeaf = store.allLeaves[1] ?? { id: 'missing-panel' }
    store.setFocus(secondLeaf.id)

    store.removeLeaf(secondLeaf.id)

    expect(store.allLeaves).toHaveLength(1)
    expect(store.allLeaves[0].source).toEqual({
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
    expect(store.focusedPanelId).toBe(store.allLeaves[0].id)

    store.removeLeaf(store.allLeaves[0].id)

    expect(store.allLeaves).toHaveLength(1)
    expect(store.allLeaves[0].source).toEqual({
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
  })
})
