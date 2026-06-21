<!--
工作区壳组件

职责：
  - 根据 active tab 渲染项目日志面板或搜索页

边界：
  - 不直接处理侧边栏点击
  - 不渲染 workspace 标签栏；MainPage 的 app 顶栏负责标签入口
  - 不实现搜索结果渲染细节
-->
<script setup lang="ts">
import PanelLayout from '@/components/Panel/PanelLayout.vue'
import ProjectOverviewPane from '@/components/Overview/ProjectOverviewPane.vue'
import RunConsolePage from '@/components/Overview/RunConsole/RunConsolePage.vue'
import SearchPage from '@/components/Search/SearchPage.vue'
import NodeCenterView from '@/components/NodeCenter/NodeCenterView.vue'
import RuntimeWorkbenchHeader from './RuntimeWorkbenchHeader.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useLogEvidenceStore } from '@/stores/logEvidence'
import { useAppI18n } from '@/i18n/useAppI18n'
import { computed, watch } from 'vue'
import type { ProjectOverviewState } from '@/stores/workspace'

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const evidenceStore = useLogEvidenceStore()
const { t } = useAppI18n()

const activeOverviewTab = computed(() => {
  const tab = workspace.activeTab
  return tab?.type === 'overview' ? tab : null
})

const overviewProject = computed(() => {
  const tab = activeOverviewTab.value
  return tab ? agentStore.projectById(tab.projectId) : null
})

const isRuntimeTab = computed(() =>
  workspace.activeTab?.type === 'project' || workspace.activeTab?.type === 'deployment',
)

watch(
  () => workspace.activeTabId,
  tabId => evidenceStore.setActiveWorkspaceTab(tabId),
  { immediate: true },
)

function updateOverviewState(state: ProjectOverviewState) {
  const tab = activeOverviewTab.value
  if (!tab) return
  workspace.updateProjectOverviewState(tab.id, state)
}
</script>

<template>
  <div class="workspace-shell">
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
    <RunConsolePage
      v-else-if="workspace.activeTab.type === 'run'"
      :project-id="workspace.activeTab.projectId"
      :pipeline-id="workspace.activeTab.pipelineId"
      :run-id="workspace.activeTab.runId"
      :mode="workspace.activeTab.mode"
    />
    <NodeCenterView
      v-else-if="workspace.activeTab.type === 'nodes'"
    />
    <ProjectOverviewPane
      v-else-if="activeOverviewTab && overviewProject"
      :key="activeOverviewTab.id"
      :project="overviewProject"
      :state="activeOverviewTab.overviewState"
      compact
      @update:state="updateOverviewState"
    />
    <div v-else-if="activeOverviewTab" class="workspace-empty">
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
