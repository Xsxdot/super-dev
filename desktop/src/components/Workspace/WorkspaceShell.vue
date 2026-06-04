<!--
工作区壳组件

职责：
  - 在右侧主内容区顶部提供工作区标签栏
  - 根据 active tab 渲染项目日志面板或搜索页

边界：
  - 不直接处理侧边栏点击
  - 不实现搜索结果渲染细节
-->
<script setup lang="ts">
import PanelLayout from '@/components/Panel/PanelLayout.vue'
import ProjectOverviewPane from '@/components/Overview/ProjectOverviewPane.vue'
import SearchPage from '@/components/Search/SearchPage.vue'
import RuntimeWorkbenchHeader from './RuntimeWorkbenchHeader.vue'
import WorkspaceTabs from './WorkspaceTabs.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppI18n } from '@/i18n/useAppI18n'
import { computed } from 'vue'

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()

const overviewProject = computed(() => {
  const tab = workspace.activeTab
  return tab?.type === 'overview' ? agentStore.projectById(tab.projectId) : null
})

const isRuntimeTab = computed(() =>
  workspace.activeTab?.type === 'project' || workspace.activeTab?.type === 'deployment',
)
</script>

<template>
  <div class="workspace-shell">
    <WorkspaceTabs v-if="workspace.tabs.length" />
    <div v-if="!workspace.activeTab" class="workspace-empty">
      <div>{{ t('shell.emptyWorkspace') }}</div>
    </div>
    <!-- 项目聚合 tab 与 deployment tab 都走 PanelLayout 分栏树（deployment tab 初始为单叶子，可拖入其他 deployment 分栏） -->
    <template v-else-if="isRuntimeTab">
      <RuntimeWorkbenchHeader />
      <PanelLayout />
    </template>
    <SearchPage
      v-else-if="workspace.activeTab.type === 'search'"
      :tab-id="workspace.activeTab.id"
    />
    <ProjectOverviewPane
      v-else-if="workspace.activeTab.type === 'overview' && overviewProject"
      :project="overviewProject"
      compact
    />
    <div v-else-if="workspace.activeTab.type === 'overview'" class="workspace-empty">
      <div>{{ t('overview.projectNotFound') }}</div>
    </div>
  </div>
</template>

<style scoped>
.workspace-shell {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.workspace-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
