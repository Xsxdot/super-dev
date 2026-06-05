<!--
项目概览嵌入面板

职责：
  - 在 workspace tab 或独立路由中展示项目运行状态、流水线、入口配置
  - 复用 RuntimeStatusTab、PipelinesTab、ProjectIngressTab

边界：
  - 不读取路由参数
  - 不直接启动或停止服务，运行态操作交给子组件和 store
-->
<script setup lang="ts">
import { ref } from 'vue'
import OverviewTabs from '@/components/Overview/OverviewTabs.vue'
import RuntimeStatusTab from '@/components/Overview/RuntimeStatusTab.vue'
import PipelinesTab from '@/components/Overview/PipelinesTab.vue'
import ProjectIngressTab from '@/components/Overview/ProjectIngressTab.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Project } from '@/api/agent'

type OverviewTab = 'runtime' | 'pipelines' | 'ingress'

defineProps<{
  project: Project
  compact?: boolean
}>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()
const activeTab = ref<OverviewTab>('runtime')

function openInstanceLogs(deploymentId: string) {
  const info = agentStore.serviceForDeployment(deploymentId)
  workspace.openDeployment(deploymentId, info ? `${info.service.name} · ${info.envName}` : deploymentId)
}
</script>

<template>
  <section class="overview-pane" :class="{ compact }">
    <header class="overview-pane-head">
      <div class="overview-title-group">
        <div class="overview-kicker">{{ t('overview.title') }}</div>
        <h1>{{ project.name }}</h1>
      </div>
      <OverviewTabs v-model="activeTab" />
    </header>
    <RuntimeStatusTab
      v-if="activeTab === 'runtime'"
      :project-id="project.id"
      :active="activeTab === 'runtime'"
      @open-logs="openInstanceLogs"
    />
    <PipelinesTab v-else-if="activeTab === 'pipelines'" :project="project" />
    <ProjectIngressTab v-else :project="project" />
  </section>
</template>

<style scoped>
.overview-pane {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.overview-pane-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 64px;
  padding: 16px 20px 12px;
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
}
.overview-title-group {
  min-width: 0;
}
.overview-kicker {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.overview-pane-head h1 {
  margin: 2px 0 0;
  overflow: hidden;
  font-size: 20px;
  font-weight: 700;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.overview-pane.compact .overview-pane-head {
  min-height: 56px;
  padding: 12px 16px 10px;
}
@media (max-width: 640px) {
  .overview-pane-head {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
