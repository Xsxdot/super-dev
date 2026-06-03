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
import { useRouter } from 'vue-router'
import { api, type Project, type ProjectPipeline, type Run } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'
import { usePipelineTemplateStore } from '@/stores/pipelineTemplate'
import ProjectPipelineEditor from '@/components/Settings/ProjectPipelineEditor.vue'
import PipelineRow from './PipelineRow.vue'
import RunHistoryList from './RunHistoryList.vue'

const props = defineProps<{ project: Project }>()
const router = useRouter()
const { t } = useAppI18n()
const templateStore = usePipelineTemplateStore()
const expanded = ref<string | null>(null)
const runsByPipeline = reactive<Record<string, Run[]>>({})
const loadingRuns = reactive<Record<string, boolean>>({})
const editing = ref(false)
const editorMode = ref<'template' | 'blank'>('blank')
const pending = ref<{ pipeline: ProjectPipeline; rollbackRun?: Run } | null>(null)
const deployError = ref<string | null>(null)
const hasPipelines = computed(() => (props.project.pipelines ?? []).length > 0)

onMounted(() => {
  void templateStore.loadTemplates().catch(() => undefined)
})

async function toggleHistory(pipeline: ProjectPipeline) {
  expanded.value = expanded.value === pipeline.id ? null : pipeline.id
  if (expanded.value !== pipeline.id || runsByPipeline[pipeline.id]) return
  loadingRuns[pipeline.id] = true
  try {
    runsByPipeline[pipeline.id] = (await api.listProjectPipelineRuns(props.project.id, pipeline.id)).items
  } finally {
    loadingRuns[pipeline.id] = false
  }
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

function openEditor(mode: 'template' | 'blank') {
  editorMode.value = mode
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
    pending.value = null
    await router.push(`/project/${props.project.id}/pipelines/${pipeline.id}/runs/${run.id}?mode=live`)
  } catch (e) {
    deployError.value = e instanceof Error ? e.message : t('overview.pipeline.deployFailed')
  }
}

function openDetail(pipeline: ProjectPipeline, run: Run) {
  void router.push(`/project/${props.project.id}/pipelines/${pipeline.id}/runs/${run.id}?mode=replay`)
}
</script>

<template>
  <section class="pipelines-tab">
    <div class="pipeline-toolbar">
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
    <div v-for="pipeline in project.pipelines ?? []" :key="pipeline.id" class="pipeline-block">
      <PipelineRow
        :pipeline="pipeline"
        :expanded="expanded === pipeline.id"
        @toggle="toggleHistory(pipeline)"
        @run="requestRun(pipeline)"
        @edit="openEditor('blank')"
      />
      <RunHistoryList
        v-if="expanded === pipeline.id"
        :runs="runsByPipeline[pipeline.id] ?? []"
        :loading="loadingRuns[pipeline.id]"
        @detail="openDetail(pipeline, $event)"
        @rollback="requestRollback(pipeline, $event)"
      />
    </div>
    <ProjectPipelineEditor
      v-if="editing"
      :project="project"
      :pipeline-templates="templateStore.templates"
      :initial-mode="editorMode"
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
  padding: 16px 20px 28px;
}
.pipeline-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}
.pipeline-toolbar button,
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
  margin-top: 8px;
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
</style>
