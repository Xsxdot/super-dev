<!--
PipelineRow：项目概览页的一条流水线行。

职责：
  - 展示流水线名称、制品类型和主要操作
  - 将执行、编辑、展开历史动作交给父组件

边界：
  - 不直接调用 API
  - 不渲染 run 历史详情
-->
<script setup lang="ts">
import type { ProjectPipeline, Run } from '@/api/agent'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  pipeline: ProjectPipeline
  expanded: boolean
  runningRun?: { id: string } | null
  latestRun?: Pick<Run, 'status' | 'artifact_version' | 'started_at'> | null
  latestDuration?: string
  latestTime?: string
}>()
const emit = defineEmits<{ run: []; edit: []; toggle: []; 'open-running': [] }>()
const { t } = useAppI18n()

function statusClass() {
  return `status-${props.latestRun?.status ?? 'idle'}`
}
</script>

<template>
  <div class="pipeline-row">
    <button type="button" data-test="pipeline-expand" class="icon-btn" @click="emit('toggle')">
      {{ expanded ? 'v' : '>' }}
    </button>
    <div class="pipeline-main">
      <div class="pipeline-name">{{ pipeline.name }}</div>
      <div class="pipeline-meta">{{ pipeline.artifact_kind || 'file' }}</div>
    </div>
    <div class="pipeline-services">
      <span v-for="service in pipeline.services ?? []" :key="service" class="service-tag">{{ service }}</span>
      <span v-if="(pipeline.services ?? []).length === 0" class="service-tag muted">--</span>
    </div>
    <span class="pipeline-status" :class="statusClass()" data-test="pipeline-status">{{ latestRun?.status || 'idle' }}</span>
    <span class="pipeline-version" data-test="pipeline-latest-version">{{ latestRun?.artifact_version || '--' }}</span>
    <span class="pipeline-duration" data-test="pipeline-latest-duration">{{ latestDuration || '--' }}</span>
    <span class="pipeline-time">{{ latestTime || '--' }}</span>
    <button
      v-if="runningRun"
      type="button"
      data-test="pipeline-running"
      class="running-badge"
      @click="emit('open-running')"
    >
      {{ t('overview.pipeline.running') }}
    </button>
    <button type="button" data-test="pipeline-run" class="primary-action" @click="emit('run')">{{ t('overview.pipeline.run') }}</button>
    <button type="button" data-test="pipeline-edit" class="text-action" @click="emit('edit')">{{ t('overview.pipeline.edit') }}</button>
  </div>
</template>

<style scoped>
.pipeline-row {
  display: grid;
  grid-template-columns: 30px minmax(220px, 1.5fr) minmax(150px, 0.9fr) 110px minmax(120px, 0.8fr) 76px 108px minmax(72px, auto) 64px 64px;
  align-items: center;
  gap: 12px;
  min-height: 62px;
  padding: 10px 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
}
.icon-btn,
.primary-action,
.text-action {
  height: 28px;
  border: 1px solid var(--border-secondary);
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.icon-btn {
  width: 30px;
  padding: 0;
  border-radius: 7px;
}
.primary-action {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
  font-weight: 700;
}
.text-action {
  background: transparent;
}
.running-badge {
  height: 24px;
  border: 1px solid color-mix(in srgb, var(--accent) 50%, transparent);
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  color: var(--accent);
  cursor: pointer;
  font-size: 11px;
  font-weight: 700;
}
.pipeline-main {
  min-width: 0;
}
.pipeline-name {
  overflow: hidden;
  font-size: 14px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pipeline-meta {
  margin-top: 4px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.pipeline-services {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  min-width: 0;
}
.service-tag {
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  padding: 3px 8px;
  color: var(--text-secondary);
  font-size: 11px;
  white-space: nowrap;
}
.service-tag.muted {
  color: var(--text-tertiary);
}
.pipeline-status {
  justify-self: start;
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  padding: 4px 8px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 700;
}
.status-success {
  border-color: color-mix(in srgb, var(--status-success) 45%, transparent);
  background: color-mix(in srgb, var(--status-success) 12%, transparent);
  color: var(--status-success);
}
.status-running,
.status-pending {
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--accent);
}
.status-failed,
.status-canceled {
  border-color: color-mix(in srgb, var(--status-failed) 45%, transparent);
  background: color-mix(in srgb, var(--status-failed) 12%, transparent);
  color: var(--status-failed);
}
.pipeline-version,
.pipeline-duration,
.pipeline-time {
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 980px) {
  .pipeline-row {
    grid-template-columns: 30px minmax(160px, 1fr) 76px 76px;
  }
  .pipeline-services,
  .pipeline-status,
  .pipeline-version,
  .pipeline-duration,
  .pipeline-time,
  .running-badge {
    display: none;
  }
}
</style>
