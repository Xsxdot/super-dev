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
import SearchPage from '@/components/Search/SearchPage.vue'
import WorkspaceTabs from './WorkspaceTabs.vue'
import { useWorkspaceStore } from '@/stores/workspace'

const workspace = useWorkspaceStore()
</script>

<template>
  <div class="workspace-shell">
    <WorkspaceTabs v-if="workspace.tabs.length" />
    <div v-if="!workspace.activeTab" class="workspace-empty">
      <div>选择左侧服务或点击项目搜索</div>
    </div>
    <PanelLayout v-else-if="workspace.activeTab.type === 'project' || workspace.activeTab.type === 'remote' || workspace.activeTab.type === 'remote-aggregate'" />
    <SearchPage
      v-else-if="workspace.activeTab.type === 'remote-search'"
      :log-source-id="workspace.activeTab.logSourceId"
      :group-key="workspace.activeTab.groupKey"
    />
    <SearchPage
      v-else-if="workspace.activeTab.type === 'search'"
      :tab-id="workspace.activeTab.id"
    />
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
