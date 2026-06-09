<!--
ProjectOverviewPage：项目级运维概览页。

职责：
  - 根据路由 project id 找到当前项目
  - 承载运行状态、流水线和入口配置三个主视图
  - 复用现有项目流水线编辑器

边界：
  - 不直接采集指标
  - 不直接执行流水线，操作交给子组件和 API store
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import ProjectOverviewPane from '@/components/Overview/ProjectOverviewPane.vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const route = useRoute()
const agentStore = useAgentStore()
const { t } = useAppI18n()
const projectId = computed(() => String(route.params.id ?? ''))
const project = computed(() => agentStore.projectById(projectId.value))
const loadingProject = ref(false)

onMounted(async () => {
  if (project.value) return
  loadingProject.value = true
  try {
    await agentStore.fetchProjects()
  } finally {
    loadingProject.value = false
  }
})
</script>

<template>
  <main class="overview-page">
    <div v-if="loadingProject && !project" class="overview-missing">{{ t('common.loading') }}</div>
    <div v-else-if="!project" class="overview-missing">{{ t('overview.projectNotFound') }}</div>
    <template v-else>
      <ProjectOverviewPane :project="project" />
    </template>
  </main>
</template>

<style scoped>
.overview-page {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.overview-missing {
  display: grid;
  height: 100%;
  place-items: center;
  color: var(--text-secondary);
  font-size: 13px;
}
</style>
