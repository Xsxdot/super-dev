// panelStore 维护面板布局树（递归 PanelNode 结构）和焦点状态。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { v4 as uuidv4 } from 'uuid'

export type PanelAxis = 'h' | 'v'

export interface PanelLeafNode {
  type: 'leaf'
  id: string
  serviceId: string | null
  projectId: string | null
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

function makeLeaf(serviceId: string | null = null, projectId: string | null = null): PanelLeafNode {
  return { type: 'leaf', id: uuidv4(), serviceId, projectId }
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
  newServiceId: string | null,
  newProjectId: string | null,
  newSide: 'first' | 'second'
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
    const newLeaf = makeLeaf(newServiceId, newProjectId)
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
    first: splitLeafById(node.first, leafId, axis, newServiceId, newProjectId, newSide),
    second: splitLeafById(node.second, leafId, axis, newServiceId, newProjectId, newSide),
  }
}

// 替换指定叶子的 scope（serviceId/projectId）
function replaceScopeById(
  node: PanelNode,
  leafId: string,
  serviceId: string | null,
  projectId: string | null
): PanelNode {
  if (node.type === 'leaf') {
    if (node.id !== leafId) return node
    return { ...node, serviceId, projectId }
  }
  return {
    ...node,
    first: replaceScopeById(node.first, leafId, serviceId, projectId),
    second: replaceScopeById(node.second, leafId, serviceId, projectId),
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
    if (raw) return JSON.parse(raw) as PanelNode
  } catch {}
  return makeLeaf()
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
    root.value = splitLeafById(root.value, leafId, axis, newServiceId, newProjectId, newSide)
    save()
    ensureFocused()
  }

  function replaceScope(leafId: string, serviceId: string | null, projectId: string | null) {
    root.value = replaceScopeById(root.value, leafId, serviceId, projectId)
    save()
  }

  function removeLeaf(leafId: string) {
    const newRoot = removeLeafById(root.value, leafId)
    root.value = newRoot ?? makeLeaf()
    save()
    ensureFocused()
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
    replaceScope,
    removeLeaf,
    targetPanelId,
  }
})
