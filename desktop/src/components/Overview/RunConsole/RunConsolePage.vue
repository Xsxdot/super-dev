<!--
RunConsolePage：单次流水线 run 的实时/回放控制台。

职责：
  - 根据路由加载 run 详情和日志
  - 左侧展示步骤/主机导航
  - 右侧展示当前选择范围的日志

边界：
  - 不触发部署或回滚
  - 不实现 pipeline 后端接口
-->
<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useRunConsoleStore } from '@/stores/runConsole'
import StepTree from './StepTree.vue'
import HostLogPanel from './HostLogPanel.vue'

const route = useRoute()
const store = useRunConsoleStore()
const projectId = computed(() => String(route.params.id ?? ''))
const pipelineId = computed(() => String(route.params.pipelineId ?? ''))
const runId = computed(() => String(route.params.runId ?? ''))
const mode = computed(() => String(route.query.mode ?? 'replay'))

onMounted(() => {
  if (mode.value === 'live') void store.loadLive(projectId.value, pipelineId.value, runId.value)
  else void store.loadReplay(projectId.value, pipelineId.value, runId.value)
})

onUnmounted(() => store.reset())
</script>

<template>
  <main class="run-console">
    <header class="run-console-head">
      <div class="run-title">
        <div class="overview-kicker">{{ mode }}</div>
        <h1>{{ store.currentRun?.artifact_version || runId }}</h1>
      </div>
      <span class="run-status">{{ store.currentRun?.status }}</span>
    </header>
    <div v-if="store.error" class="run-console-error">{{ store.error }}</div>
    <div class="run-console-body">
      <StepTree
        :steps="store.currentRun?.step_runs ?? []"
        :selected-step="store.selectedStep"
        :selected-host="store.selectedHost"
        @select-step="store.select($event)"
        @select-host="(step, host) => store.select(step, host)"
      />
      <HostLogPanel
        :logs="store.logs"
        :selected-step="store.selectedStep"
        :selected-host="store.selectedHost"
      />
    </div>
  </main>
</template>

<style scoped>
.run-console {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.run-console-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border-secondary);
}
.run-title {
  min-width: 0;
}
.overview-kicker {
  color: var(--text-tertiary);
  font-size: 11px;
  text-transform: uppercase;
}
.run-title h1 {
  overflow: hidden;
  margin: 2px 0 0;
  font-size: 16px;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-status {
  color: var(--status-running);
  font-size: 12px;
  font-weight: 700;
}
.run-console-error {
  padding: 8px 16px;
  border-bottom: 1px solid var(--border-secondary);
  color: var(--status-failed);
  font-size: 12px;
}
.run-console-body {
  display: grid;
  flex: 1;
  grid-template-columns: minmax(220px, 28%) minmax(0, 1fr);
  min-height: 0;
}
</style>
