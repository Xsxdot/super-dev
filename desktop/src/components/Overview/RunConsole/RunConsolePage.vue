<!--
RunConsolePage：单次流水线 run 的实时/回放控制台。

职责：
  - 根据 workspace tab 参数加载 run 详情和日志
  - 左侧展示步骤/主机导航
  - 右侧展示当前选择范围的日志

边界：
  - 不触发部署或回滚
  - 不实现 pipeline 后端接口
-->
<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRunConsoleStore } from '@/stores/runConsole'
import StepTree from './StepTree.vue'
import HostLogPanel from './HostLogPanel.vue'
import FailureBanner from './FailureBanner.vue'

const props = defineProps<{
  projectId: string
  pipelineId: string
  runId: string
  mode: 'live' | 'replay'
}>()

const store = useRunConsoleStore()
const state = computed(() => store.stateFor(props.runId))
const visibleLogs = computed(() => store.visibleLogs(props.runId))

function loadRun() {
  if (props.mode === 'live') void store.loadLive(props.projectId, props.pipelineId, props.runId)
  else void store.loadReplay(props.projectId, props.pipelineId, props.runId)
}

function selectFailureLogs(step: string, host: string) {
  store.select(props.runId, step, host)
}

onMounted(loadRun)
watch(() => [props.projectId, props.pipelineId, props.runId, props.mode] as const, loadRun)
</script>

<template>
  <main class="run-console">
    <header class="run-console-head">
      <div class="run-title">
        <div class="overview-kicker">{{ mode }}</div>
        <h1>{{ state.currentRun?.artifact_version || runId }}</h1>
      </div>
      <span class="run-status" :class="state.currentRun?.status">
        <span v-if="state.currentRun?.status === 'running'" class="status-spinner" />
        {{ state.currentRun?.status || mode }}
      </span>
    </header>
    <div v-if="state.error" class="run-console-error">{{ state.error }}</div>
    <FailureBanner
      :run="state.currentRun"
      @view-logs="selectFailureLogs"
    />
    <div class="run-console-body">
      <StepTree
        :steps="state.currentRun?.step_runs ?? []"
        :selected-step="state.selectedStep"
        :selected-host="state.selectedHost"
        @select-step="store.select(runId, $event)"
        @select-host="(step, host) => store.select(runId, step, host)"
      />
      <HostLogPanel
        :logs="visibleLogs"
        :selected-step="state.selectedStep"
        :selected-host="state.selectedHost"
        :loading="state.loading"
        :running="state.currentRun?.status === 'running'"
      />
    </div>
  </main>
</template>

<style scoped>
.run-console {
  display: flex;
  flex-direction: column;
  height: 100%;
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
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.run-status.running {
  color: var(--accent);
}
.run-status.success {
  color: var(--status-running);
}
.run-status.failed {
  color: var(--status-failed);
}
.status-spinner {
  width: 12px;
  height: 12px;
  border: 2px solid color-mix(in srgb, var(--accent) 25%, transparent);
  border-top-color: var(--accent);
  border-radius: 999px;
  animation: spin 0.9s linear infinite;
}
@keyframes spin {
  to { transform: rotate(360deg); }
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
