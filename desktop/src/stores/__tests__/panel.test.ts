import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { usePanelStore } from '../panel'

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

  it('replaceSource：中心落点可将本地面板替换为远程监听来源且保持焦点', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    store.setFocus(leafId)

    ;((store as any).replaceSource ?? (() => undefined))(leafId, {
      type: 'remote-log-source',
      logSourceId: 'remote-prod',
      groupKey: 'all',
    })

    expect(store.allLeaves).toHaveLength(1)
    expect((store.allLeaves[0] as any).source).toEqual({
      type: 'remote-log-source',
      logSourceId: 'remote-prod',
      groupKey: 'all',
    })
    expect(store.focusedPanelId).toBe(leafId)
  })

  it('splitLeafWithSource：远程来源可拖到本地面板边缘形成分栏并获得焦点', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    ;((store as any).replaceSource ?? (() => undefined))(leafId, {
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })

    ;((store as any).splitLeafWithSource ?? (() => undefined))(
      leafId,
      'h',
      {
        type: 'remote-log-source',
        logSourceId: 'remote-prod',
        groupKey: 'all',
      },
      'first',
    )

    expect(store.root.type).toBe('split')
    expect(store.allLeaves.map(leaf => (leaf as any).source)).toEqual([
      {
        type: 'remote-log-source',
        logSourceId: 'remote-prod',
        groupKey: 'all',
      },
      {
        type: 'local-service',
        projectId: 'project-A',
        serviceId: 'svc-api',
      },
    ])
    expect(store.focusedPanelId).toBe(store.allLeaves[0].id)
  })

  it('removeLeaf：关闭焦点远程面板后保留兄弟来源且不允许最后一个面板消失', () => {
    const store = usePanelStore()
    const leafId = store.allLeaves[0].id
    ;((store as any).replaceSource ?? (() => undefined))(leafId, {
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
    ;((store as any).splitLeafWithSource ?? (() => undefined))(
      leafId,
      'h',
      {
        type: 'remote-log-source',
        logSourceId: 'remote-prod',
        groupKey: 'all',
      },
      'second',
    )
    const remoteLeaf = store.allLeaves[1] ?? { id: 'missing-remote-panel' }
    store.setFocus(remoteLeaf.id)

    store.removeLeaf(remoteLeaf.id)

    expect(store.allLeaves).toHaveLength(1)
    expect((store.allLeaves[0] as any).source).toEqual({
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
    expect(store.focusedPanelId).toBe(store.allLeaves[0].id)

    store.removeLeaf(store.allLeaves[0].id)

    expect(store.allLeaves).toHaveLength(1)
    expect((store.allLeaves[0] as any).source).toEqual({
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
  })
})
