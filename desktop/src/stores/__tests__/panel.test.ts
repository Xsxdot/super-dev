import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { usePanelStore, projectIdFromPanelSource } from '../panel'

describe('projectIdFromPanelSource', () => {
  const ctx = {
    logSourceById: (id: string) => ({
      'ls-bound': { project_id: 'project-a' },
      'ls-service': { service_id: 'service-api' },
    }[id]),
    serviceById: (id: string) => ({
      'service-api': { project_id: 'project-a' },
    }[id]),
  }

  it('本地服务来源返回 projectId', () => {
    expect(projectIdFromPanelSource(
      { type: 'local-service', projectId: 'project-a', serviceId: 'svc-1' },
      ctx,
    )).toBe('project-a')
  })

  it('远程聚合来源返回 source.projectId', () => {
    expect(projectIdFromPanelSource(
      { type: 'remote-aggregate', logSourceIds: ['ls-a'], groupKey: 'all', projectId: 'project-a' },
      ctx,
    )).toBe('project-a')
  })

  it('远程单任务从 log source.project_id 解析', () => {
    expect(projectIdFromPanelSource(
      { type: 'remote-log-source', logSourceId: 'ls-bound', groupKey: 'all' },
      ctx,
    )).toBe('project-a')
  })

  it('远程单任务从绑定 service_id 反查项目', () => {
    expect(projectIdFromPanelSource(
      { type: 'remote-log-source', logSourceId: 'ls-service', groupKey: 'all' },
      ctx,
    )).toBe('project-a')
  })

  it('未绑定项目时返回 null', () => {
    expect(projectIdFromPanelSource(
      { type: 'remote-log-source', logSourceId: 'ls-free', groupKey: 'all' },
      ctx,
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

  it('splitLeafWithSource：重复拖入相同远程来源时焦点落到新建 leaf', () => {
    const store = usePanelStore()
    const localLeafId = store.allLeaves[0].id
    const remoteSource = {
      type: 'remote-log-source' as const,
      logSourceId: 'remote-prod',
      groupKey: 'all',
    }

    ;((store as any).replaceSource ?? (() => undefined))(localLeafId, {
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
    ;((store as any).splitLeafWithSource ?? (() => undefined))(
      localLeafId,
      'h',
      remoteSource,
      'first',
    )
    const oldRemoteLeafId = store.allLeaves[0].id

    ;((store as any).splitLeafWithSource ?? (() => undefined))(
      localLeafId,
      'v',
      remoteSource,
      'second',
    )

    const duplicateLeaf = store.allLeaves.find(
      leaf => leaf.id !== localLeafId && leaf.id !== oldRemoteLeafId && (leaf as any).source?.type === 'remote-log-source',
    )
    expect(duplicateLeaf).toBeTruthy()
    expect(store.focusedPanelId).toBe(duplicateLeaf?.id)
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
