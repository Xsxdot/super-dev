// panelStore 维护面板布局树（递归 PanelNode 结构）和焦点状态。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { v4 as uuidv4 } from 'uuid'

export type PanelAxis = 'h' | 'v'

export const MAX_PANEL_LEAVES = 4

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

export interface PanelSourceProjectContext {
  logSourceById: (id: string) => { project_id?: string; service_id?: string } | undefined
  serviceById: (id: string) => { project_id?: string } | undefined
}

/** 从面板来源解析项目 ID，供远程监听复用绑定项目的 LogRule。 */
export function projectIdFromPanelSource(
  source: PanelSource | null,
  ctx: PanelSourceProjectContext,
): string | null {
  if (!source) return null
  if (source.type === 'local-service' || source.type === 'local-project') {
    return source.projectId
  }
  if (source.type === 'remote-aggregate') {
    return source.projectId ?? null
  }
  if (source.type === 'remote-log-source') {
    const logSource = ctx.logSourceById(source.logSourceId)
    if (!logSource) return null
    if (logSource.project_id) return logSource.project_id
    if (logSource.service_id) {
      return ctx.serviceById(logSource.service_id)?.project_id ?? null
    }
  }
  return null
}

function sortedIds(ids: string[]): string[] {
  return [...ids].sort()
}

function isRemotePanelSource(source: PanelSource | null): source is Extract<PanelSource, { type: 'remote-log-source' | 'remote-aggregate' }> {
  return source?.type === 'remote-log-source' || source?.type === 'remote-aggregate'
}

export function isSamePanelSource(a: PanelSource | null, b: PanelSource | null): boolean {
  if (!a || !b || a.type !== b.type) return false
  if (a.type === 'local-service' && b.type === 'local-service') {
    return a.projectId === b.projectId && a.serviceId === b.serviceId
  }
  if (a.type === 'local-project' && b.type === 'local-project') {
    return a.projectId === b.projectId
  }
  if (a.type === 'remote-log-source' && b.type === 'remote-log-source') {
    return a.logSourceId === b.logSourceId && a.groupKey === b.groupKey
  }
  if (a.type === 'remote-aggregate' && b.type === 'remote-aggregate') {
    if (a.groupKey !== b.groupKey) return false
    if (a.serviceId && b.serviceId) return a.serviceId === b.serviceId
    return sortedIds(a.logSourceIds).join('\0') === sortedIds(b.logSourceIds).join('\0')
  }
  return false
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
  newLeaf: PanelLeafNode,
  newSide: 'first' | 'second'
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
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
    first: splitLeafById(node.first, leafId, axis, newLeaf, newSide),
    second: splitLeafById(node.second, leafId, axis, newLeaf, newSide),
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

  function findLeafByEquivalentRemoteSource(source: PanelSource | null): PanelLeafNode | null {
    if (!isRemotePanelSource(source)) return null
    return allLeaves.value.find(leaf => isSamePanelSource(leaf.source, source)) ?? null
  }

  function focusEquivalentRemoteSource(source: PanelSource | null): boolean {
    const leaf = findLeafByEquivalentRemoteSource(source)
    if (!leaf) return false
    setFocus(leaf.id)
    return true
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

  function canAddPanelLeaf(): boolean {
    return allLeaves.value.length < MAX_PANEL_LEAVES
  }

  function splitLeafWithSource(
    leafId: string,
    axis: PanelAxis,
    source: PanelSource | null,
    newSide: 'first' | 'second'
  ) {
    if (focusEquivalentRemoteSource(source)) return
    if (!canAddPanelLeaf()) return
    const newLeaf = makeLeafFromSource(source)
    root.value = splitLeafById(root.value, leafId, axis, newLeaf, newSide)
    save()
    ensureFocused()
    if (allLeaves.value.some(leaf => leaf.id === newLeaf.id)) focusedPanelId.value = newLeaf.id
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
    findLeafByEquivalentRemoteSource,
    focusEquivalentRemoteSource,
    canAddPanelLeaf,
    splitLeaf,
    splitLeafWithSource,
    replaceScope,
    replaceSource,
    removeLeaf,
    setRoot,
    targetPanelId,
  }
})
