<!--
SingleProjectPipelineForm：单条项目流水线表单。

职责：
  - 编辑一条 ProjectPipeline 的基础信息、服务绑定和模板化 pipeline
  - 将变更通过 update:pipeline 返回父组件

边界：
  - 不保存项目配置
  - 不执行流水线或读取运行历史
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { ArtifactKind, Pipeline, PipelineTemplateSummary, ProjectPipeline } from '@/api/agent'
import BuildConfigBar from './BuildConfigBar.vue'
import DeployTargetReadonly from './DeployTargetReadonly.vue'
import PipelineEnvMatrix from './PipelineEnvMatrix.vue'
import PipelineTemplateWizard from './PipelineTemplateWizard.vue'

type HostOption = { id: string; name: string; is_self?: boolean }
type ConfigPanel = 'build' | 'variables' | 'deploy'

const props = defineProps<{
  pipeline: ProjectPipeline
  services: Array<{ id: string; name: string }>
  hosts: HostOption[]
  templates: PipelineTemplateSummary[]
  targetsByEnv?: Record<string, string[]>
  availableEnvironments?: string[]
  initialMode?: 'template' | 'blank'
  withStructureRail?: boolean
  hidePreviewStrip?: boolean
  onViewTemplate?: (template: PipelineTemplateSummary, apply: () => void) => void
}>()

const emit = defineEmits<{ 'update:pipeline': [ProjectPipeline] }>()
const { t } = useAppI18n()
const draft = ref<ProjectPipeline>({ ...props.pipeline, services: [...(props.pipeline.services ?? [])] })
const wizard = ref<InstanceType<typeof PipelineTemplateWizard> | null>(null)
const activeConfigPanel = ref<ConfigPanel | null>(null)
const reservedNames = ['workspace', 'output', 'artifacts', 'version', 'env', 'date', 'time', 'run_temp_dir', 'sync_mode']
const targetsByEnv = computed(() => props.targetsByEnv ?? {})
const selfHostId = computed(() => props.hosts.find(host => host.is_self)?.id ?? '')
const selectedBuilderHostId = computed(() => draft.value.roles?.builder?.hosts?.[0] ?? selfHostId.value)
const selectedBuilderIsLocal = computed(() => selectedBuilderHostId.value === '' || (selfHostId.value !== '' && selectedBuilderHostId.value === selfHostId.value))
const effectiveSyncMode = computed(() => selectedBuilderIsLocal.value ? 'transfer' : (draft.value.sync_mode ?? 'transfer'))
const builderHostLabel = computed(() => {
  const hostID = selectedBuilderHostId.value
  if (!hostID) return t('common.local')
  return props.hosts.find(host => host.id === hostID)?.name ?? hostID
})
const builderSummary = computed(() => {
  const items = [builderHostLabel.value]
  if (!selectedBuilderIsLocal.value) items.push(t(`settings.pipeline.syncMode_${effectiveSyncMode.value}`))
  return items
})
const envNames = computed(() => Object.keys(draft.value.environments ?? {}).filter(Boolean))
const globalVarCount = computed(() => Object.keys(draft.value.variables ?? {}).length)
const visibleRoleNames = computed(() => Object.keys(draft.value.roles ?? {}).filter(name => name !== 'builder' && !name.endsWith('_runner')))
const variableSummary = computed(() => [
  `${t('settings.pipeline.globalVars')} ${globalVarCount.value}`,
  envNames.value.length ? envNames.value.join('/') : '0',
  `${t('settings.pipeline.runGroups')} ${visibleRoleNames.value.length}`,
])
const deployEnvCount = computed(() => Object.keys(targetsByEnv.value).filter(Boolean).length)
const deployHostCount = computed(() => Object.values(targetsByEnv.value).reduce((sum, hosts) => sum + hosts.length, 0))
const deploySummary = computed(() => [
  t('settings.pipeline.readonly'),
  t('settings.pipeline.envCount', { count: deployEnvCount.value }),
  t('settings.pipeline.hostCount', { count: deployHostCount.value }),
])

watch(() => props.pipeline, (pipeline) => {
  draft.value = { ...pipeline, services: [...(pipeline.services ?? [])] }
})

function patch(partial: Partial<ProjectPipeline>) {
  draft.value = {
    ...draft.value,
    ...partial,
    services: partial.services ? [...partial.services] : [...(draft.value.services ?? [])],
  }
  emit('update:pipeline', draft.value)
}

function toggleService(name: string, checked: boolean) {
  const set = new Set(draft.value.services ?? [])
  if (checked) set.add(name)
  else set.delete(name)
  patch({ services: [...set] })
}

function setPipeline(pipeline: Pipeline | undefined) {
  patch({ pipeline: pipeline ?? {} })
}

function setBuilderHost(hostId: string) {
  const roles = { ...(draft.value.roles ?? {}) }
  roles.builder = { hosts: hostId ? [hostId] : [] }
  const hostIsLocal = hostId === '' || (selfHostId.value !== '' && hostId === selfHostId.value)
  patch(hostIsLocal ? { roles, sync_mode: 'transfer' } : { roles })
}

function toggleConfigPanel(panel: ConfigPanel) {
  activeConfigPanel.value = activeConfigPanel.value === panel ? null : panel
}

function saveDraft() {
  wizard.value?.saveTemplate()
}

defineExpose({ saveDraft })
</script>

<template>
  <section class="single-pipeline-form" :class="{ 'with-structure-rail': withStructureRail }">
    <div class="single-pipeline-topbar" data-test="single-pipeline-form-topbar">
      <div class="topbar-field name-field">
        <label class="field-row">
          <span>{{ t('settings.pipeline.name') }} / Pipeline name</span>
          <input
            class="settings-input"
            data-test="single-pipeline-name"
            :value="draft.name"
            @input="patch({ name: ($event.target as HTMLInputElement).value })"
          />
        </label>
      </div>

      <div class="topbar-field">
        <label class="field-row">
          <span>{{ t('settings.pipeline.artifactKind') }} / Artifact kind</span>
          <div class="artifact-segment" data-test="single-pipeline-artifact-kind">
            <button
              v-for="kind in (['file', 'image'] as ArtifactKind[])"
              :key="kind"
              type="button"
              :class="{ active: (draft.artifact_kind || 'file') === kind }"
              @click="patch({ artifact_kind: kind })"
            >
              {{ kind }}
            </button>
          </div>
        </label>
      </div>

      <div class="topbar-field services-field">
        <div class="field-row">
          <span>{{ t('settings.pipeline.services') }} / Services</span>
          <div class="service-list">
            <label v-for="service in services" :key="service.id || service.name" class="service-item">
              <input
                type="checkbox"
                :data-test="`single-pipeline-service-${service.name}`"
                :checked="(draft.services ?? []).includes(service.name)"
                @change="toggleService(service.name, ($event.target as HTMLInputElement).checked)"
              />
              {{ service.name }}
            </label>
          </div>
        </div>
      </div>
    </div>

    <div class="single-pipeline-config-stack" data-test="pipeline-config-stack">
      <section
        class="config-panel"
        :class="{ open: activeConfigPanel === 'build' }"
        data-test="pipeline-config-panel-build"
      >
        <button type="button" class="config-panel-head" data-test="pipeline-config-toggle-build" @click="toggleConfigPanel('build')">
          <span class="config-panel-main">
            <span class="config-chevron">{{ activeConfigPanel === 'build' ? '▾' : '▸' }}</span>
            <span>
              <span class="config-title">{{ t('settings.pipeline.builderHost') }} / Build machine</span>
              <span class="config-hint">{{ selectedBuilderIsLocal ? t('settings.pipeline.builderLocalHint') : t('settings.pipeline.builderRemoteHint') }}</span>
            </span>
          </span>
          <span class="config-badges">
            <span v-for="item in builderSummary" :key="item" class="config-badge">{{ item }}</span>
          </span>
        </button>
        <div v-if="activeConfigPanel === 'build'" class="config-panel-body" data-test="pipeline-config-body-build">
          <BuildConfigBar
            :builder-host-id="selectedBuilderHostId"
            :sync-mode="effectiveSyncMode"
            :sync-command="draft.sync_command ?? ''"
            :hosts="hosts"
            @update:builder-host-id="setBuilderHost"
            @update:sync-mode="patch({ sync_mode: $event })"
            @update:sync-command="patch({ sync_command: $event })"
          />
        </div>
      </section>

      <section
        class="config-panel"
        :class="{ open: activeConfigPanel === 'variables' }"
        data-test="pipeline-config-panel-variables"
      >
        <button type="button" class="config-panel-head" data-test="pipeline-config-toggle-variables" @click="toggleConfigPanel('variables')">
          <span class="config-panel-main">
            <span class="config-chevron">{{ activeConfigPanel === 'variables' ? '▾' : '▸' }}</span>
            <span>
              <span class="config-title">{{ t('settings.pipeline.envVars') }} / Variables</span>
              <span class="config-hint">{{ t('settings.pipeline.variableCopyHint') }}</span>
            </span>
          </span>
          <span class="config-badges">
            <span v-for="item in variableSummary" :key="item" class="config-badge">{{ item }}</span>
          </span>
        </button>
        <div v-if="activeConfigPanel === 'variables'" class="config-panel-body" data-test="pipeline-config-body-variables">
          <PipelineEnvMatrix
            :variables="draft.variables ?? {}"
            :environments="draft.environments ?? {}"
            :roles="draft.roles"
            :available-environments="availableEnvironments"
            :hosts="hosts"
            :reserved-names="reservedNames"
            :standalone="false"
            @update:variables="patch({ variables: $event })"
            @update:environments="patch({ environments: $event })"
            @update:roles="patch({ roles: $event })"
          />
        </div>
      </section>

      <section
        class="config-panel"
        :class="{ open: activeConfigPanel === 'deploy' }"
        data-test="pipeline-config-panel-deploy"
      >
        <button type="button" class="config-panel-head" data-test="pipeline-config-toggle-deploy" @click="toggleConfigPanel('deploy')">
          <span class="config-panel-main">
            <span class="config-chevron">{{ activeConfigPanel === 'deploy' ? '▾' : '▸' }}</span>
            <span>
              <span class="config-title">{{ t('settings.pipeline.deployTarget') }} / Deployment targets</span>
              <span class="config-hint">{{ t('settings.pipeline.deployTargetHint') }}</span>
            </span>
          </span>
          <span class="config-badges">
            <span v-for="item in deploySummary" :key="item" class="config-badge">{{ item }}</span>
          </span>
        </button>
        <div v-if="activeConfigPanel === 'deploy'" class="config-panel-body" data-test="pipeline-config-body-deploy">
          <DeployTargetReadonly :targets-by-env="targetsByEnv" />
        </div>
      </section>
    </div>

    <div class="single-pipeline-lower">
      <div v-if="withStructureRail" class="single-pipeline-rail-slot">
        <slot name="rail" />
      </div>
      <div class="single-pipeline-main" data-test="pipeline-editor-stage-area">
        <PipelineTemplateWizard
          ref="wizard"
          :model-value="draft.pipeline"
          :pipeline-roles="draft.roles ?? pipeline.roles"
          :templates="templates"
          :hosts="hosts"
          :initial-mode="initialMode"
          :hide-preview-strip="hidePreviewStrip"
          :on-view-template="onViewTemplate"
          @update:model-value="setPipeline"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.single-pipeline-form {
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr);
  min-width: 0;
  min-height: 0;
  height: 100%;
}
.single-pipeline-topbar {
  display: grid;
  grid-template-columns: 370px 206px minmax(0, 1fr);
  align-items: start;
  gap: 22px;
  min-height: 98px;
  border: 0;
  border-bottom: 1px solid #263240;
  border-radius: 0;
  padding: 18px 18px 16px;
  background: #121922;
}
.with-structure-rail .single-pipeline-topbar {
  border-top: 0;
  border-right: 0;
  border-left: 0;
  border-radius: 0;
}
.single-pipeline-lower {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: 0;
  height: 100%;
}
.with-structure-rail .single-pipeline-lower {
  grid-template-columns: 290px minmax(0, 1fr);
}
.single-pipeline-rail-slot {
  min-width: 0;
  min-height: 0;
}
.single-pipeline-main {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.single-pipeline-config-stack {
  display: grid;
  background: #0e141c;
  border-bottom: 1px solid #263240;
}

.config-panel + .config-panel {
  border-top: 1px solid #1f2b38;
}

.config-panel.open {
  background: #0f1720;
}

.config-panel-head {
  width: 100%;
  min-height: 48px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  padding: 9px 18px;
  text-align: left;
}

.config-panel-head:hover {
  background: rgba(255, 255, 255, 0.02);
}

.config-panel-main {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;
}

.config-chevron {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  background: #17212c;
  color: var(--text-primary);
  font-size: 11px;
}

.config-title,
.config-hint {
  display: block;
}

.config-title {
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 650;
}

.config-hint {
  margin-top: 3px;
  color: var(--text-tertiary, #667);
  font-size: 11px;
  font-weight: 500;
}

.config-badges {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}

.config-badge {
  min-height: 22px;
  display: inline-flex;
  align-items: center;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: #15202b;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 550;
  padding: 2px 8px;
  white-space: nowrap;
}

.config-panel-body {
  padding: 0 18px 14px 52px;
}
.field-row {
  display: grid;
  grid-template-columns: 1fr;
  align-items: start;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 500;
}
.service-list {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 18px;
  min-height: 38px;
  align-items: center;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  padding: 0 12px;
  background: #0b1118;
}
.service-item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 500;
}
.service-item input,
.target-item input,
.boolean-field input {
  accent-color: var(--accent);
}
.artifact-segment {
  display: grid;
  grid-template-columns: 1fr 1fr;
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  overflow: hidden;
  background: #0b1118;
}
.artifact-segment button {
  height: 38px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}
.artifact-segment button + button {
  border-left: 1px solid var(--border-secondary);
}
.artifact-segment button.active {
  background: linear-gradient(180deg, #2587ff, #176de9);
  color: #fff;
  font-weight: 600;
}
@media (max-width: 1040px) {
  .single-pipeline-topbar {
    grid-template-columns: 1fr;
  }
  .field-row {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
}
@media (max-width: 1100px) {
  .with-structure-rail .single-pipeline-lower {
    grid-template-columns: 1fr;
  }
  .single-pipeline-rail-slot {
    display: none;
  }
}
</style>
