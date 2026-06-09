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
import { computed, onMounted, reactive, ref } from 'vue'
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
const overviewPipeline = computed(() =>
  (props.project.pipelines ?? []).find(pipeline => pipeline.id === expanded.value) ?? props.project.pipelines?.[0] ?? null,
)

onMounted(() => {
  void templateStore.loadTemplates().catch(() => undefined)
  for (const pipeline of props.project.pipelines ?? []) {
    void loadRunsForPipeline(pipeline).catch(() => undefined)
  }
})

function runTitle(pipeline: ProjectPipeline, run: Run) {
  const version = run.artifact_version ? `#${run.artifact_version}` : run.id.slice(0, 8)
  return `${pipeline.name} · ${version}`
}

function runningRun(pipeline: ProjectPipeline): Run | null {
  return (runsByPipeline[pipeline.id] ?? []).find(run => run.status === 'running') ?? null
}

function latestRun(pipeline: ProjectPipeline): Run | null {
  return runsByPipeline[pipeline.id]?.[0] ?? null
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

function phaseCount(pipeline: ProjectPipeline | null, phase: 'build' | 'deploy' | 'finally') {
  return pipeline?.pipeline?.[phase]?.length ?? 0
}

function includeSteps(pipeline: ProjectPipeline | null) {
  const phases = [pipeline?.pipeline?.build ?? [], pipeline?.pipeline?.deploy ?? [], pipeline?.pipeline?.finally ?? []]
  return phases.flat().filter(step => step.type === 'include').slice(0, 4)
}

async function loadRunsForPipeline(pipeline: ProjectPipeline) {
  loadingRuns[pipeline.id] = true
  try {
    runsByPipeline[pipeline.id] = (await api.listProjectPipelineRuns(props.project.id, pipeline.id)).items
  } finally {
    loadingRuns[pipeline.id] = false
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
  if (expanded.value !== pipeline.id || runsByPipeline[pipeline.id]) return
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
    runsByPipeline[pipeline.id] = [run, ...(runsByPipeline[pipeline.id] ?? []).filter(item => item.id !== run.id)]
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
        <div class="pipeline-console-eyebrow">{{ t('overview.pipeline.consoleEyebrow') }}</div>
        <div class="pipeline-console-title">{{ t('overview.pipeline.consoleTitle') }}</div>
      </div>
      <div class="pipeline-console-summary" data-test="pipeline-console-summary">
        <span>{{ t('overview.pipeline.totalCount', { count: consoleSummary.total }) }}</span>
        <span>{{ t('overview.pipeline.successCount', { count: consoleSummary.success }) }}</span>
        <span>{{ t('overview.pipeline.runningCount', { count: consoleSummary.running }) }}</span>
        <span>{{ t('overview.pipeline.failedCount', { count: consoleSummary.failed }) }}</span>
      </div>
      <button type="button" data-test="pipeline-add" @click="openEditor('blank')">{{ t('overview.pipeline.add') }}</button>
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
            :runs="runsByPipeline[pipeline.id] ?? []"
            :loading="loadingRuns[pipeline.id]"
            @detail="openDetail(pipeline, $event)"
            @rollback="requestRollback(pipeline, $event)"
          />
        </div>
      </div>

      <aside v-if="overviewPipeline" class="pipeline-overview-card" data-test="pipeline-overview">
        <h3>{{ t('overview.pipeline.overviewTitle') }}</h3>
        <dl>
          <div>
            <dt>{{ t('settings.pipeline.name') }}</dt>
            <dd>{{ overviewPipeline.name }}</dd>
          </div>
          <div>
            <dt>{{ t('settings.pipeline.artifactKind') }}</dt>
            <dd>{{ overviewPipeline.artifact_kind || 'file' }}</dd>
          </div>
          <div>
            <dt>{{ t('settings.pipeline.services') }}</dt>
            <dd>{{ (overviewPipeline.services ?? []).join(', ') || '--' }}</dd>
          </div>
        </dl>

        <div class="overview-section">
          <div class="overview-section-title">{{ t('overview.pipeline.phasesTitle') }}</div>
          <div class="phase-count"><span>{{ t('settings.pipeline.phases.build') }}</span><strong>{{ phaseCount(overviewPipeline, 'build') }}</strong></div>
          <div class="phase-count"><span>{{ t('settings.pipeline.phases.deploy') }}</span><strong>{{ phaseCount(overviewPipeline, 'deploy') }}</strong></div>
          <div class="phase-count"><span>{{ t('settings.pipeline.phases.finally') }}</span><strong>{{ phaseCount(overviewPipeline, 'finally') }}</strong></div>
        </div>

        <div class="overview-section">
          <div class="overview-section-title">{{ t('overview.pipeline.templatesTitle') }}</div>
          <div v-if="includeSteps(overviewPipeline).length === 0" class="overview-empty">--</div>
          <div v-for="step in includeSteps(overviewPipeline)" :key="step.name" class="template-chip">
            {{ step.name }}
          </div>
        </div>
      </aside>
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
  height: calc(100vh - 65px);
  overflow: auto;
  padding: 20px 24px 32px;
}
.pipeline-console-head {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto auto;
  align-items: center;
  gap: 14px;
  margin-bottom: 14px;
}
.pipeline-console-eyebrow {
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.pipeline-console-title {
  margin-top: 3px;
  color: var(--text-primary);
  font-size: 20px;
  font-weight: 800;
}
.pipeline-console-summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.pipeline-console-summary span {
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  padding: 5px 9px;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  font-size: 11px;
  white-space: nowrap;
}
.pipeline-console-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  gap: 16px;
  align-items: start;
}
.pipeline-table-card,
.pipeline-overview-card {
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: color-mix(in srgb, var(--bg-elevated) 82%, transparent);
}
.pipeline-table-card {
  overflow: hidden;
}
.pipeline-table-head {
  display: grid;
  grid-template-columns: minmax(260px, 1.5fr) minmax(150px, 0.9fr) 110px minmax(120px, 0.8fr) 76px 108px 170px;
  gap: 12px;
  align-items: center;
  min-height: 42px;
  padding: 0 14px 0 54px;
  border-bottom: 1px solid var(--border-secondary);
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.pipeline-overview-card {
  padding: 14px;
}
.pipeline-overview-card h3 {
  margin: 0 0 12px;
  color: var(--text-primary);
  font-size: 13px;
}
.pipeline-overview-card dl {
  display: grid;
  gap: 10px;
  margin: 0;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--border-secondary);
}
.pipeline-overview-card dt {
  color: var(--text-tertiary);
  font-size: 11px;
}
.pipeline-overview-card dd {
  margin: 4px 0 0;
  color: var(--text-primary);
  font-size: 12px;
  line-height: 1.4;
}
.overview-section {
  padding-top: 14px;
}
.overview-section-title {
  margin-bottom: 8px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
}
.phase-count {
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--text-secondary);
  font-size: 12px;
}
.phase-count + .phase-count {
  margin-top: 7px;
}
.template-chip {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  padding: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}
.template-chip + .template-chip {
  margin-top: 7px;
}
.overview-empty {
  color: var(--text-tertiary);
  font-size: 12px;
}
.pipeline-console-head button,
.deploy-dialog button {
  height: 28px;
  padding: 0 12px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.pipeline-block + .pipeline-block {
  margin-top: 10px;
}
.pipeline-empty-card {
  padding: 18px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
}
.pipeline-empty-card h2 {
  margin: 0;
  font-size: 15px;
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

@media (max-width: 900px) {
  .pipeline-console-head {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
  .pipeline-console-summary {
    flex-wrap: wrap;
  }
  .pipeline-console-grid {
    grid-template-columns: 1fr;
  }
  .pipeline-table-head,
  .pipeline-overview-card {
    display: none;
  }
}
</style>
