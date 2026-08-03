<!--
项目概览嵌入面板

职责：
  - 在 workspace tab 或独立路由中展示项目运行状态、流水线、入口配置和项目配置
  - 复用 RuntimeStatusTab、PipelinesTab、ProjectIngressTab、ProjectConfigSurface
  - 头部下方嵌入 ProjectHomeCard（开发环境归属卡），compact 时该卡收敛为
    只有徽标一行，避免挤占 workspace tab 头部空间

边界：
  - 不读取路由参数
  - 不直接启动或停止服务，运行态操作交给子组件和 store
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import OverviewTabs from '@/components/Overview/OverviewTabs.vue'
import ProjectHomeCard from '@/components/Overview/ProjectHomeCard.vue'
import RuntimeStatusTab from '@/components/Overview/RuntimeStatusTab.vue'
import PipelinesTab from '@/components/Overview/PipelinesTab.vue'
import ProjectIngressTab from '@/components/Overview/ProjectIngressTab.vue'
import ProjectConfigSurface from '@/components/Settings/ProjectConfigSurface.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Project } from '@/api/agent'
import type { ProjectConfigSurfaceState, ProjectOverviewState, ProjectOverviewSubtab } from '@/stores/workspace'

const props = defineProps<{
  project: Project
  compact?: boolean
  state?: ProjectOverviewState
}>()
const emit = defineEmits<{ 'update:state': [ProjectOverviewState] }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()
const localState = ref<ProjectOverviewState>({ activeTab: 'runtime' })
const overviewState = computed(() => props.state ?? localState.value)
const activeTab = computed<ProjectOverviewSubtab>({
  get: () => overviewState.value.activeTab,
  set: activeTab => patchOverviewState({ activeTab }),
})

function patchOverviewState(patch: Partial<ProjectOverviewState>) {
  const next = { ...overviewState.value, ...patch }
  if (props.state) {
    emit('update:state', next)
    return
  }
  localState.value = next
}

function updateConfigState(config: ProjectConfigSurfaceState) {
  patchOverviewState({ config })
}

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
      <div class="overview-head-actions">
        <OverviewTabs v-model="activeTab" />
      </div>
    </header>
    <ProjectHomeCard :project="project" :compact="compact" />
    <RuntimeStatusTab
      v-if="activeTab === 'runtime'"
      :project="project"
      :active="activeTab === 'runtime'"
      @open-logs="openInstanceLogs"
    />
    <PipelinesTab v-else-if="activeTab === 'pipelines'" :project="project" />
    <ProjectIngressTab v-else-if="activeTab === 'ingress'" :project="project" />
    <ProjectConfigSurface
      v-else
      :project="props.project"
      :state="overviewState.config"
      embedded
      @update:state="updateConfigState"
    />
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
  background:
    radial-gradient(circle at 72% 10%, rgba(30, 122, 255, 0.12), transparent 25%),
    linear-gradient(180deg, #060a10 0%, #090f16 45%, #070b11 100%);
  color: var(--text-primary);
}
.overview-pane-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  min-height: 92px;
  padding: 20px 22px 14px;
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
}
.overview-title-group {
  min-width: 0;
}
.overview-head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}
.overview-kicker {
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.overview-pane-head h1 {
  margin: 10px 0 0;
  overflow: hidden;
  font-size: 28px;
  font-weight: 800;
  line-height: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.overview-pane.compact .overview-pane-head {
  min-height: 70px;
  padding: 12px 16px 10px;
}
@media (max-width: 640px) {
  .overview-pane-head {
    align-items: stretch;
    flex-direction: column;
  }
  .overview-head-actions {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
