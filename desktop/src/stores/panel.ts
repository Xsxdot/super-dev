// panelStore 维护面板布局树（递归 PanelNode 结构）和焦点状态。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { v4 as uuidv4 } from 'uuid'

export type PanelAxis = 'h' | 'v'

export const MAX_PANEL_LEAVES = 4

// PanelSource 现在只有 deployment 单一来源：deployment_id 是全系统唯一的运行/日志单元标识。
// 历史上的 local-service / local-project 来源已废弃。
export type PanelSource =
  | { type: 'deployment'; deploymentId: string }

export interface PanelLeafNode {
  type: 'leaf'
  id: string
  // serviceId 保留字段名，但语义为「日志订阅键」即 deploymentId。命名沿用历史，避免大面积改字段名。
  serviceId: string | null
  // projectId 已废弃，恒为 null：项目归属由 agentStore 通过 deploymentId 反查。保留字段仅为兼容旧布局快照。
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

// sourceFromScope 由旧的 (serviceId, projectId) 二元组构造来源。
// projectId 已不再产生任何来源；serviceId（语义为 deploymentId）有值时构造 deployment 来源。
function sourceFromScope(serviceId: string | null, _projectId: string | null): PanelSource | null {
  if (serviceId) return { type: 'deployment', deploymentId: serviceId }
  return null
}

function scopeFromSource(source: PanelSource | null): { serviceId: string | null; projectId: string | null } {
  if (!source) return { serviceId: null, projectId: null }
  if (source.type === 'deployment') return { serviceId: source.deploymentId, projectId: null }
  return { serviceId: null, projectId: null }
}

/**
 * projectIdFromPanelSource 从面板来源解析项目 ID。
 *
 * deployment 单源下来源不再直接携带项目；项目归属由 agentStore 通过 deploymentId 反查。
 * 故此函数恒返回 null，仅为兼容旧调用点保留签名。
 */
export function projectIdFromPanelSource(_source: PanelSource | null): string | null {
  return null
}

export function isSamePanelSource(a: PanelSource | null, b: PanelSource | null): boolean {
  if (!a || !b || a.type !== b.type) return false
  if (a.type === 'deployment' && b.type === 'deployment') {
    return a.deploymentId === b.deploymentId
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

/** createDeploymentPanelRoot 返回一个以指定 deployment 为来源的单叶子布局根，用作 deployment tab 的初始分栏树。 */
export function createDeploymentPanelRoot(deploymentId: string): PanelLeafNode {
  return makeLeafFromSource({ type: 'deployment', deploymentId })
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
    // 容错旧布局快照：localStorage 里可能存有 local-service / local-project 来源。
    // 只接受 deployment 来源，其余一律按 serviceId（语义=deploymentId）重新推导，推导不出则退化为 null。
    const rawSource = node.source as PanelSource | { type?: string; serviceId?: string } | null
    const source =
      rawSource && rawSource.type === 'deployment'
        ? (rawSource as PanelSource)
        : sourceFromScope(node.serviceId ?? (rawSource as { serviceId?: string })?.serviceId ?? null, null)
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

  function canAddPanelLeaf(): boolean {
    return allLeaves.value.length < MAX_PANEL_LEAVES
  }

  function splitLeafWithSource(
    leafId: string,
    axis: PanelAxis,
    source: PanelSource | null,
    newSide: 'first' | 'second'
  ) {
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
