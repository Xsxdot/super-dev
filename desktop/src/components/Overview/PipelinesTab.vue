<!--
PipelinesTab：项目概览页的流水线列表和历史入口。

职责：
  - 列出 project.pipelines
  - 触发执行、历史加载、详情跳转和回滚
  - 复用 ProjectPipelineEditor 编辑/新增流水线

边界：
  - 不实现流水线编辑器
  - 不执行 pipeline 引擎逻辑
-->
<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { Icon } from '@iconify/vue'
import { api, type Project, type ProjectPipeline, type Run } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import { usePipelineTemplateStore } from '@/stores/pipelineTemplate'
import { useWorkspaceStore } from '@/stores/workspace'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import PipelineRow from './PipelineRow.vue'
import RunHistoryList from './RunHistoryList.vue'

const props = defineProps<{ project: Project }>()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()
const templateStore = usePipelineTemplateStore()
const expanded = ref<string | null>(props.project.pipelines?.[0]?.id ?? null)
const runsByPipeline = reactive<Record<string, Run[]>>({})
const loadingRuns = reactive<Record<string, boolean>>({})
const refreshingRuns = ref(false)
const editing = ref(false)
const editorMode = ref<'template' | 'blank'>('blank')
const editingPipelineId = ref<string | undefined>(undefined)
const pending = ref<{ pipeline: ProjectPipeline; rollbackRun?: Run } | null>(null)
const deployError = ref<string | null>(null)
const hasPipelines = computed(() => (props.project.pipelines ?? []).length > 0)
const consoleSummary = computed(() => {
  const pipelines = props.project.pipelines ?? []
  const latestRuns = pipelines.map(pipeline => latestRun(pipeline)).filter(Boolean) as Run[]
  return {
    total: pipelines.length,
    success: latestRuns.filter(run => run.status === 'success').length,
    running: latestRuns.filter(run => run.status === 'running').length,
    failed: latestRuns.filter(run => run.status === 'failed').length,
  }
})
const summaryItems = computed(() => [
  { key: 'total', label: t('overview.pipeline.totalLabel'), value: consoleSummary.value.total, tone: 'neutral', icon: 'lucide:workflow' },
  { key: 'success', label: t('overview.pipeline.successLabel'), value: consoleSummary.value.success, tone: 'success', icon: 'lucide:circle-check' },
  { key: 'failed', label: t('overview.pipeline.failedLabel'), value: consoleSummary.value.failed, tone: 'failed', icon: 'lucide:circle-x' },
  { key: 'running', label: t('overview.pipeline.runningLabel'), value: consoleSummary.value.running, tone: 'running', icon: 'lucide:refresh-cw' },
])

onMounted(() => {
  void templateStore.loadTemplates().catch(() => undefined)
  void loadProjectRuns().catch(() => undefined)
})

watch(() => props.project.id, () => {
  expanded.value = props.project.pipelines?.[0]?.id ?? null
  void loadProjectRuns().catch(() => undefined)
})

function runTitle(pipeline: ProjectPipeline, run: Run) {
  const version = run.artifact_version ? `#${run.artifact_version}` : run.id.slice(0, 8)
  return `${pipeline.name} · ${version}`
}

function runningRun(pipeline: ProjectPipeline): Run | null {
  return runsForPipeline(pipeline).find(run => run.status === 'running') ?? null
}

function latestRun(pipeline: ProjectPipeline): Run | null {
  return runsForPipeline(pipeline)[0] ?? null
}

function duration(run?: Run | null) {
  if (!run?.finished_at || !run.started_at) return '--'
  return `${Math.max(0, Math.round((run.finished_at - run.started_at) / 1000))}s`
}

function recentTime(run?: Run | null) {
  if (!run?.started_at) return '--'
  const started = run.started_at < 1_000_000_000_000 ? run.started_at * 1000 : run.started_at
  const seconds = Math.max(0, Math.round((Date.now() - started) / 1000))
  if (seconds < 60) return seconds <= 5 ? 'Just now' : `${seconds}s ago`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}

function pipelineRunKey(projectId: string, pipelineId: string) {
  return `${encodeURIComponent(projectId)}:${encodeURIComponent(pipelineId)}`
}

function keyForPipeline(pipeline: ProjectPipeline) {
  return pipelineRunKey(props.project.id, pipeline.id)
}

function runsForPipeline(pipeline: ProjectPipeline) {
  return runsByPipeline[keyForPipeline(pipeline)] ?? []
}

function loadingForPipeline(pipeline: ProjectPipeline) {
  return loadingRuns[keyForPipeline(pipeline)] ?? false
}

async function loadRunsForPipeline(pipeline: ProjectPipeline, projectId = props.project.id) {
  const key = pipelineRunKey(projectId, pipeline.id)
  loadingRuns[key] = true
  try {
    runsByPipeline[key] = (await api.listProjectPipelineRuns(projectId, pipeline.id)).items
  } finally {
    loadingRuns[key] = false
  }
}

async function loadProjectRuns() {
  const projectId = props.project.id
  await Promise.all((props.project.pipelines ?? []).map(pipeline => loadRunsForPipeline(pipeline, projectId)))
}

async function refreshRuns() {
  refreshingRuns.value = true
  try {
    await loadProjectRuns()
  } finally {
    refreshingRuns.value = false
  }
}

function openRunConsole(pipeline: ProjectPipeline, run: Run, mode: 'live' | 'replay') {
  workspace.openRunConsole({
    projectId: props.project.id,
    pipelineId: pipeline.id,
    runId: run.id,
    mode,
    title: runTitle(pipeline, run),
  })
}

async function toggleHistory(pipeline: ProjectPipeline) {
  expanded.value = expanded.value === pipeline.id ? null : pipeline.id
  if (expanded.value !== pipeline.id || runsByPipeline[keyForPipeline(pipeline)]) return
  await loadRunsForPipeline(pipeline)
}

function defaultEnvName() {
  return props.project.environments?.find(e => e.is_dev)?.name ?? props.project.environments?.[0]?.name ?? 'dev'
}

function requestRun(pipeline: ProjectPipeline) {
  pending.value = { pipeline }
  deployError.value = null
}

function requestRollback(pipeline: ProjectPipeline, run: Run) {
  pending.value = { pipeline, rollbackRun: run }
  deployError.value = null
}

function openEditor(mode: 'template' | 'blank', pipelineId?: string) {
  editorMode.value = mode
  editingPipelineId.value = pipelineId
  editing.value = true
}

async function confirmDeploy() {
  if (!pending.value) return
  const { pipeline, rollbackRun } = pending.value
  try {
    const run = await api.deployProjectPipeline(props.project.id, pipeline.id, {
      env_name: rollbackRun?.env_name || defaultEnvName(),
      artifact_version: rollbackRun?.artifact_version,
    })
    const key = keyForPipeline(pipeline)
    runsByPipeline[key] = [run, ...(runsByPipeline[key] ?? []).filter(item => item.id !== run.id)]
    pending.value = null
    openRunConsole(pipeline, run, 'live')
  } catch (e) {
    deployError.value = e instanceof Error ? e.message : t('overview.pipeline.deployFailed')
  }
}

function openDetail(pipeline: ProjectPipeline, run: Run) {
  openRunConsole(pipeline, run, run.status === 'running' ? 'live' : 'replay')
}
</script>

<template>
  <section class="pipelines-tab">
    <div class="pipeline-console-head">
      <div>
        <div class="pipeline-console-title-row">
          <div class="pipeline-console-title">{{ t('overview.pipeline.consoleTitle') }}</div>
          <span class="pipeline-console-subtitle">{{ t('overview.pipeline.consoleSubtitle') }}</span>
        </div>
      </div>
      <div class="pipeline-console-actions">
        <button type="button" class="pipeline-add-btn" data-test="pipeline-add" @click="openEditor('blank')">
          <Icon icon="lucide:plus" aria-hidden="true" />
          {{ t('overview.pipeline.add') }}
        </button>
        <button
          type="button"
          class="pipeline-refresh-btn"
          data-test="pipeline-refresh"
          :disabled="refreshingRuns"
          :aria-label="t('overview.pipeline.refresh')"
          @click="refreshRuns"
        >
          <Icon icon="lucide:refresh-cw" aria-hidden="true" :class="{ spinning: refreshingRuns }" />
        </button>
      </div>
    </div>
    <div class="pipeline-console-summary" data-test="pipeline-console-summary">
      <article
        v-for="item in summaryItems"
        :key="item.key"
        class="pipeline-stat"
        :class="`tone-${item.tone}`"
        :data-test="`pipeline-stat-${item.key}`"
      >
        <Icon class="pipeline-stat-icon" :icon="item.icon" aria-hidden="true" />
        <strong>{{ item.value }}</strong>
        <span>{{ item.label }}</span>
      </article>
    </div>
    <article v-if="!hasPipelines" class="pipeline-empty-card" data-test="pipeline-empty">
      <h2>{{ t('overview.pipeline.emptyTitle') }}</h2>
      <p>{{ t('overview.pipeline.emptyDescription') }}</p>
      <div class="empty-actions">
        <button type="button" class="primary-empty-action" data-test="pipeline-create-from-template" @click="openEditor('template')">
          {{ t('overview.pipeline.createFromTemplate') }}
        </button>
        <button type="button" data-test="pipeline-create-blank" @click="openEditor('blank')">
          {{ t('overview.pipeline.createBlank') }}
        </button>
      </div>
    </article>
    <div v-if="hasPipelines" class="pipeline-console-grid">
      <div class="pipeline-table-card">
        <div class="pipeline-table-scroll" data-test="pipeline-table-scroll">
          <div class="pipeline-table-inner">
            <div class="pipeline-table-head" data-test="pipeline-table-head">
              <span>{{ t('overview.pipeline.columnPipeline') }}</span>
              <span>{{ t('overview.pipeline.columnServices') }}</span>
              <span>{{ t('overview.pipeline.columnStatus') }}</span>
              <span>{{ t('overview.pipeline.columnVersion') }}</span>
              <span>{{ t('overview.pipeline.columnDuration') }}</span>
              <span>{{ t('overview.pipeline.columnRecent') }}</span>
              <span>{{ t('overview.pipeline.columnActions') }}</span>
            </div>

            <div v-for="pipeline in project.pipelines ?? []" :key="pipeline.id" class="pipeline-block">
              <PipelineRow
                :pipeline="pipeline"
                :expanded="expanded === pipeline.id"
                :running-run="runningRun(pipeline)"
                :latest-run="latestRun(pipeline)"
                :latest-duration="duration(latestRun(pipeline))"
                :latest-time="recentTime(latestRun(pipeline))"
                @toggle="toggleHistory(pipeline)"
                @run="requestRun(pipeline)"
                @edit="openEditor('blank', pipeline.id)"
                @open-running="() => { const run = runningRun(pipeline); if (run) openRunConsole(pipeline, run, 'live') }"
              />
              <RunHistoryList
                v-if="expanded === pipeline.id"
                :runs="runsForPipeline(pipeline)"
                :loading="loadingForPipeline(pipeline)"
                :artifact-kind="pipeline.artifact_kind || 'file'"
                @detail="openDetail(pipeline, $event)"
                @rollback="requestRollback(pipeline, $event)"
              />
            </div>
          </div>
        </div>
      </div>
    </div>
    <div v-if="hasPipelines" class="pipeline-timezone" data-test="pipeline-timezone">
      {{ t('overview.pipeline.timezone') }}
    </div>
    <ProjectPipelineEditor
      v-if="editing"
      :project="project"
      :pipeline-templates="templateStore.templates"
      :initial-mode="editorMode"
      :pipeline-id="editingPipelineId"
      @cancel="editing = false"
      @saved="editing = false"
    />
    <div v-if="pending" class="deploy-dialog">
      <div>{{ pending.rollbackRun ? t('overview.pipeline.rollback') : t('overview.pipeline.run') }} · {{ pending.pipeline.name }}</div>
      <div v-if="deployError" class="deploy-error">{{ deployError }}</div>
      <button type="button" data-test="deploy-confirm" @click="confirmDeploy">{{ t('overview.pipeline.confirm') }}</button>
      <button type="button" @click="pending = null">{{ t('overview.pipeline.cancel') }}</button>
    </div>
  </section>
</template>

<style scoped>
.pipelines-tab {
  height: calc(100vh - 92px);
  overflow: auto;
  padding: 24px 20px 28px;
  background:
    radial-gradient(circle at 72% 10%, rgba(30, 122, 255, 0.1), transparent 24%),
    linear-gradient(180deg, rgba(13, 19, 27, 0.78), rgba(7, 11, 17, 0.96));
}
.pipeline-console-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin-bottom: 18px;
}
.pipeline-console-title-row {
  display: flex;
  align-items: baseline;
  gap: 20px;
  min-width: 0;
}
.pipeline-console-subtitle {
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 500;
}
.pipeline-console-title {
  color: var(--text-primary);
  font-size: 20px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1.2;
}
.pipeline-console-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
.pipeline-console-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  min-width: 0;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
  margin-bottom: 20px;
  background: linear-gradient(180deg, #111922, #0e151d);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
}
.pipeline-stat {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 56px;
  padding: 0 24px;
}
.pipeline-stat + .pipeline-stat {
  border-left: 1px solid rgba(29, 39, 51, 0.98);
}
.pipeline-stat strong {
  color: var(--text-primary);
  font-size: 18px;
  font-weight: 700;
}
.pipeline-stat span:last-child {
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}
.pipeline-stat:not(:first-child) strong {
  order: 3;
}
.pipeline-stat:not(:first-child) span:last-child {
  order: 2;
}
.pipeline-stat-icon {
  width: 18px;
  height: 18px;
  color: var(--text-tertiary);
}
.tone-success .pipeline-stat-icon,
.tone-success strong {
  color: #47d764;
}
.tone-failed .pipeline-stat-icon,
.tone-failed strong {
  color: #ff4b55;
}
.tone-running .pipeline-stat-icon,
.tone-running strong {
  color: #ffbd17;
}
.pipeline-console-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-items: start;
}
.pipeline-table-card {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  overflow: hidden;
  background: #0b1118;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.025), 0 18px 48px rgba(0, 0, 0, 0.14);
}
.pipeline-table-scroll {
  width: 100%;
  overflow-x: auto;
  overflow-y: hidden;
}
.pipeline-table-scroll::-webkit-scrollbar {
  height: 10px;
}
.pipeline-table-scroll::-webkit-scrollbar-thumb {
  border: 3px solid rgba(13, 18, 26, 0.88);
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.36);
}
.pipeline-table-inner {
  --pipeline-actions-width: 126px;
  --pipeline-name-width: 360px;
  --pipeline-services-width: 280px;
  --pipeline-version-width: 176px;
  min-width: 1342px;
}
.pipeline-table-head {
  display: grid;
  grid-template-columns: var(--pipeline-name-width) var(--pipeline-services-width) 112px var(--pipeline-version-width) 72px minmax(140px, 1fr) var(--pipeline-actions-width);
  gap: 0;
  align-items: center;
  min-height: 47px;
  padding: 0 16px 0 60px;
  border-bottom: 1px solid var(--border-secondary);
  background: #0e151d;
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 500;
}
.pipeline-add-btn,
.pipeline-refresh-btn,
.deploy-dialog button {
  height: 36px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}
.pipeline-add-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 82px;
  padding: 0 16px;
  border-color: var(--accent);
  background: linear-gradient(180deg, #2385ff 0%, #1669e3 100%);
  color: #fff;
}
.pipeline-add-btn span {
  font-size: 19px;
  line-height: 1;
}
.pipeline-add-btn svg {
  width: 17px;
  height: 17px;
}
.pipeline-refresh-btn {
  display: inline-grid;
  place-items: center;
  width: 40px;
  padding: 0;
  color: var(--text-secondary);
}
.pipeline-refresh-btn svg {
  width: 16px;
  height: 16px;
}
.pipeline-refresh-btn:disabled {
  cursor: wait;
  opacity: 0.72;
}
.spinning {
  display: inline-block;
  animation: pipeline-spin 1s linear infinite;
}
.pipeline-block + .pipeline-block {
  margin-top: 0;
  border-top: 1px solid var(--border-secondary);
}
.pipeline-empty-card {
  padding: 18px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
}
.pipeline-empty-card h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}
.pipeline-empty-card p {
  max-width: 560px;
  margin: 8px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
  line-height: 1.6;
}
.empty-actions {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}
.empty-actions button {
  height: 28px;
  padding: 0 12px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.empty-actions .primary-empty-action {
  color: #fff;
  background: var(--accent);
  border-color: var(--accent);
}
.deploy-dialog {
  position: fixed;
  right: 20px;
  bottom: 20px;
  z-index: 20;
  min-width: 260px;
  padding: 14px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-overlay);
  color: var(--text-primary);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.32);
}
.deploy-dialog button {
  margin-top: 12px;
  margin-right: 8px;
}
.deploy-error {
  margin-top: 8px;
  color: var(--status-failed);
  font-size: 12px;
}
.pipeline-timezone {
  margin: 24px 0 20px;
  color: var(--text-tertiary);
  font-size: 12px;
  text-align: center;
}
@keyframes pipeline-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 900px) {
  .pipeline-console-head {
    align-items: stretch;
    flex-direction: column;
  }
  .pipeline-console-summary {
    grid-template-columns: 1fr 1fr;
  }
  .pipeline-console-grid {
    grid-template-columns: 1fr;
  }
}
</style>
