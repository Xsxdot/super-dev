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
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import ProjectOverviewPane from '@/components/Overview/ProjectOverviewPane.vue'
import { useAppI18n } from '@/i18n/useAppI18n'

const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const { t } = useAppI18n()
const projectId = computed(() => String(route.params.id ?? ''))
const project = computed(() => agentStore.projectById(projectId.value))
</script>

<template>
  <main class="overview-page">
    <div v-if="!project" class="overview-missing">{{ t('overview.projectNotFound') }}</div>
    <template v-else>
      <button
        class="overview-back"
        data-test="overview-back"
        type="button"
        @click="router.push('/')"
      >
        <span aria-hidden="true">←</span>
        {{ t('common.back') }}
      </button>
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
.overview-back {
  position: absolute;
  top: 16px;
  left: 20px;
  z-index: 2;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  padding: 0 10px;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}
.overview-back:hover {
  border-color: var(--border);
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.overview-page :deep(.overview-pane-head) {
  padding-left: 128px;
}
.overview-missing {
  display: grid;
  height: 100%;
  place-items: center;
  color: var(--text-secondary);
  font-size: 13px;
}
</style>
