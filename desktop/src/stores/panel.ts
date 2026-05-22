// panelStore 维护面板布局树（递归 PanelNode 结构）和焦点状态。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { v4 as uuidv4 } from 'uuid'

export type PanelAxis = 'h' | 'v'

export type PanelSource =
  | { type: 'local-service'; projectId: string; serviceId: string }
  | { type: 'local-project'; projectId: string }
  | { type: 'remote-log-source'; logSourceId: string; groupKey: string }
  | { type: 'remote-aggregate'; logSourceIds: string[]; groupKey: string; projectId?: string; serviceId?: string; serviceName?: string }

export interface PanelLeafNode {
  type: 'leaf'
  id: string
  serviceId: string | null
  projectId: string | null
  source: PanelSource | null
}

export interface PanelSplitNode {
  type: 'split'
  id: string
  axis: PanelAxis
  ratio: number  // 0~1，first 面板占比
  first: PanelNode
  second: PanelNode
}

export type PanelNode = PanelLeafNode | PanelSplitNode

function sourceFromScope(serviceId: string | null, projectId: string | null): PanelSource | null {
  if (serviceId && projectId) return { type: 'local-service', projectId, serviceId }
  if (projectId) return { type: 'local-project', projectId }
  return null
}

function scopeFromSource(source: PanelSource | null): { serviceId: string | null; projectId: string | null } {
  if (!source) return { serviceId: null, projectId: null }
  if (source.type === 'local-service') return { serviceId: source.serviceId, projectId: source.projectId }
  if (source.type === 'local-project') return { serviceId: null, projectId: source.projectId }
  return { serviceId: null, projectId: null }
}

function makeLeafFromSource(source: PanelSource | null = null): PanelLeafNode {
  const scope = scopeFromSource(source)
  return { type: 'leaf', id: uuidv4(), ...scope, source }
}

function makeLeaf(serviceId: string | null = null, projectId: string | null = null): PanelLeafNode {
  return makeLeafFromSource(sourceFromScope(serviceId, projectId))
}

export function createEmptyPanelRoot(): PanelLeafNode {
  return makeLeaf()
}

function getAllLeaves(node: PanelNode): PanelLeafNode[] {
  if (node.type === 'leaf') return [node]
  return [...getAllLeaves(node.first), ...getAllLeaves(node.second)]
}

// 在树中找到 id 对应的叶子节点，替换为 split 节点
function splitLeafById(
  node: PanelNode,
  leafId: string,
  axis: PanelAxis,
  source: PanelSource | null,
  newSide: 'first' | 'second'
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
    const newLeaf = makeLeafFromSource(source)
    const split: PanelSplitNode = {
      type: 'split',
      id: uuidv4(),
      axis,
      ratio: 0.5,
      first: newSide === 'first' ? newLeaf : node,
      second: newSide === 'second' ? newLeaf : node,
    }
    return split
  }
  return {
    ...node,
    first: splitLeafById(node.first, leafId, axis, source, newSide),
    second: splitLeafById(node.second, leafId, axis, source, newSide),
  }
}

function replaceSourceById(
  node: PanelNode,
  leafId: string,
  source: PanelSource | null
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
    const scope = scopeFromSource(source)
    return { ...node, ...scope, source }
  }
  return {
    ...node,
    first: replaceSourceById(node.first, leafId, source),
    second: replaceSourceById(node.second, leafId, source),
  }
}

// 删除指定叶子，用兄弟节点替代父 split 节点
function removeLeafById(node: PanelNode, leafId: string): PanelNode | null {
  if (node.type === 'leaf') {
    return node.id === leafId ? null : node
  }
  const newFirst = removeLeafById(node.first, leafId)
  const newSecond = removeLeafById(node.second, leafId)
  if (!newFirst) return newSecond
  if (!newSecond) return newFirst
  return { ...node, first: newFirst, second: newSecond }
}

const STORAGE_KEY = 'superdev:panel-layout'

function loadLayout(): PanelNode {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return normalizePanelNode(JSON.parse(raw) as PanelNode)
  } catch {}
  return makeLeaf()
}

function normalizePanelNode(node: PanelNode): PanelNode {
  if (node.type === 'leaf') {
    const source = node.source ?? sourceFromScope(node.serviceId, node.projectId)
    const scope = scopeFromSource(source)
    return { ...node, ...scope, source }
  }
  return {
    ...node,
    first: normalizePanelNode(node.first),
    second: normalizePanelNode(node.second),
  }
}

export const usePanelStore = defineStore('panel', () => {
  const root = ref<PanelNode>(loadLayout())
  const focusedPanelId = ref<string | null>(null)

  const allLeaves = computed(() => getAllLeaves(root.value))

  function save() {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(root.value))
  }

  function ensureFocused() {
    const leaves = allLeaves.value
    if (!leaves.length) return
    if (focusedPanelId.value && leaves.some(l => l.id === focusedPanelId.value)) return
    focusedPanelId.value = leaves[0].id
  }

  function setFocus(panelId: string) {
    focusedPanelId.value = panelId
  }

  function splitLeaf(
    leafId: string,
    axis: PanelAxis,
    newServiceId: string | null,
    newProjectId: string | null,
    newSide: 'first' | 'second'
  ) {
    splitLeafWithSource(leafId, axis, sourceFromScope(newServiceId, newProjectId), newSide)
  }

  function splitLeafWithSource(
    leafId: string,
    axis: PanelAxis,
    source: PanelSource | null,
    newSide: 'first' | 'second'
  ) {
    root.value = splitLeafById(root.value, leafId, axis, source, newSide)
    save()
    ensureFocused()
    const sourceKey = JSON.stringify(source)
    const created = allLeaves.value.find(leaf => leaf.id !== leafId && JSON.stringify(leaf.source) === sourceKey)
    if (created) focusedPanelId.value = created.id
  }

  function replaceScope(leafId: string, serviceId: string | null, projectId: string | null) {
    replaceSource(leafId, sourceFromScope(serviceId, projectId))
  }

  function replaceSource(leafId: string, source: PanelSource | null) {
    root.value = replaceSourceById(root.value, leafId, source)
    save()
  }

  function removeLeaf(leafId: string) {
    if (allLeaves.value.length <= 1) return
    const newRoot = removeLeafById(root.value, leafId)
    root.value = newRoot ?? makeLeaf()
    save()
    ensureFocused()
  }

  function setRoot(nextRoot: PanelNode, nextFocusedPanelId: string | null = null) {
    root.value = normalizePanelNode(nextRoot)
    focusedPanelId.value = nextFocusedPanelId
    ensureFocused()
    save()
  }

  // 当前焦点面板的目标 panelId（有焦点用焦点，否则用第一个）
  function targetPanelId(): string | null {
    const leaves = allLeaves.value
    if (!leaves.length) return null
    const focused = focusedPanelId.value
    if (focused && leaves.some(l => l.id === focused)) return focused
    return leaves[0].id
  }

  ensureFocused()

  return {
    root,
    focusedPanelId,
    allLeaves,
    setFocus,
    splitLeaf,
    splitLeafWithSource,
    replaceScope,
    replaceSource,
    removeLeaf,
    setRoot,
    targetPanelId,
  }
})
