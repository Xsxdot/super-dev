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
})
