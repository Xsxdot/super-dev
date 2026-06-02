<!--
ProjectOverviewPage：项目级运维概览页。

职责：
  - 根据路由 project id 找到当前项目
  - 承载运行状态和流水线两个主视图
  - 复用现有项目流水线编辑器

边界：
  - 不直接采集指标
  - 不直接执行流水线，操作交给子组件和 API store
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import OverviewTabs from '@/components/Overview/OverviewTabs.vue'
import RuntimeStatusTab from '@/components/Overview/RuntimeStatusTab.vue'
import PipelinesTab from '@/components/Overview/PipelinesTab.vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const route = useRoute()
const agentStore = useAgentStore()
const { t } = useAppI18n()
const activeTab = ref<'runtime' | 'pipelines'>('runtime')
const projectId = computed(() => String(route.params.id ?? ''))
const project = computed(() => agentStore.projectById(projectId.value))
</script>

<template>
  <main class="overview-page">
    <div v-if="!project" class="overview-missing">{{ t('overview.projectNotFound') }}</div>
    <template v-else>
      <header class="overview-head">
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
      />
      <PipelinesTab v-else :project="project" />
    </template>
  </main>
</template>

<style scoped>
.overview-page {
  height: 100vh;
  overflow: hidden;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.overview-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 64px;
  padding: 16px 20px 12px;
  border-bottom: 1px solid var(--border-secondary);
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
.overview-head h1 {
  margin: 2px 0 0;
  overflow: hidden;
  font-size: 20px;
  font-weight: 700;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.overview-missing {
  display: grid;
  height: 100%;
  place-items: center;
  color: var(--text-secondary);
  font-size: 13px;
}
@media (max-width: 640px) {
  .overview-head {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
