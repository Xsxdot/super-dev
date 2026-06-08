<!--
工作区标签栏组件

职责：
  - 渲染项目标签和搜索标签
  - 切换/关闭 workspace tab

边界：
  - 不渲染标签内容
  - 不负责创建标签，创建由侧边栏触发
-->
<script setup lang="ts">
import { useAppI18n } from '@/i18n/useAppI18n'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore, type WorkspaceTab } from '@/stores/workspace'

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()

function tabKind(tab: WorkspaceTab): string {
  if (tab.type === 'nodes') return '◎'
  if (tab.type === 'overview') return '◉'
  if (tab.type === 'project') return '▣'
  if (tab.type === 'search') return '⌕'
  return '●'
}

function projectName(projectId: string): string {
  return agentStore.projectById(projectId)?.name ?? projectId
}

function displayTitle(tab: WorkspaceTab): string {
  if (tab.type === 'nodes') return t('nodeCenter.title')
  if (tab.type === 'overview') return t('overview.title')
  if (tab.type === 'project') return projectName(tab.projectId)
  if (tab.type === 'search') {
    return tab.query
      ? t('search.tabTitleForQuery', { query: tab.query })
      : t('search.tabTitleForProject', { project: projectName(tab.projectId) })
  }
  return tab.title
}
</script>

<template>
  <div class="workspace-tabs">
    <button
      v-for="tab in workspace.tabs"
      :key="tab.id"
      class="workspace-tab"
      :class="{
        active: workspace.activeTabId === tab.id,
        search: tab.type === 'search',
        overview: tab.type === 'overview',
        nodes: tab.type === 'nodes',
      }"
      @click="workspace.activateTab(tab.id)"
    >
      <span class="tab-kind">{{ tabKind(tab) }}</span>
      <span class="tab-title">{{ displayTitle(tab) }}</span>
      <span class="tab-close" @click.stop="workspace.closeTab(tab.id)">×</span>
    </button>
  </div>
</template>

<style scoped>
.workspace-tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding: 3px 8px 0;
  background: var(--bg-primary);
  border-bottom: 1px solid var(--border-secondary);
  overflow-x: auto;
  flex-shrink: 0;
}
.workspace-tab {
  display: flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  max-width: 220px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-bottom: none;
  border-radius: 5px 5px 0 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}
.workspace-tab.active {
  background: var(--bg-elevated);
  border-color: var(--border-secondary);
  color: var(--text-primary);
}
.workspace-tab.search .tab-kind { color: #58a6ff; }
.workspace-tab.overview .tab-kind { color: #8b949e; }
.workspace-tab.nodes .tab-kind { color: #3fb950; }
.tab-title {
  overflow: hidden;
  text-overflow: ellipsis;
}
.tab-close {
  color: var(--text-tertiary);
  padding: 0 2px;
}
.tab-close:hover { color: var(--text-primary); }
</style>
