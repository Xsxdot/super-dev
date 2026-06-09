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
import { Icon } from '@iconify/vue'
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

function statusLabel() {
  return props.latestRun?.status ? t(`overview.pipeline.status.${props.latestRun.status}`) : '--'
}

function statusIcon() {
  switch (props.latestRun?.status) {
    case 'success':
      return 'lucide:circle-check'
    case 'failed':
      return 'lucide:circle-x'
    case 'running':
    case 'pending':
      return 'lucide:refresh-cw'
    case 'canceled':
      return 'lucide:ban'
    default:
      return 'lucide:circle'
  }
}
</script>

<template>
  <div class="pipeline-row">
    <button type="button" data-test="pipeline-expand" class="icon-btn" @click="emit('toggle')">
      <Icon :icon="expanded ? 'lucide:chevron-down' : 'lucide:chevron-right'" aria-hidden="true" />
    </button>
    <div class="pipeline-main">
      <span class="pipeline-state-dot" :class="statusClass()" aria-hidden="true"></span>
      <div class="pipeline-name-group">
        <div class="pipeline-name">{{ pipeline.name }}</div>
      </div>
    </div>
    <div class="pipeline-services">
      <span v-for="service in pipeline.services ?? []" :key="service" class="service-tag">{{ service }}</span>
      <span v-if="(pipeline.services ?? []).length === 0" class="service-tag muted">--</span>
    </div>
    <span class="pipeline-status" :class="statusClass()" data-test="pipeline-status">
      <Icon class="status-icon" :icon="statusIcon()" aria-hidden="true" />
      {{ statusLabel() }}
    </span>
    <span class="pipeline-version" data-test="pipeline-latest-version">{{ latestRun?.artifact_version || '--' }}</span>
    <span class="pipeline-duration" data-test="pipeline-latest-duration">{{ latestDuration || '--' }}</span>
    <span class="pipeline-time">{{ latestTime || '--' }}</span>
    <div class="pipeline-actions">
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
      <button type="button" class="more-action" :aria-label="t('overview.pipeline.moreActions')">
        <Icon icon="lucide:more-vertical" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.pipeline-row {
  --pipeline-actions-width: 126px;
  --pipeline-name-width: 360px;
  --pipeline-services-width: 280px;
  --pipeline-version-width: 176px;
  display: grid;
  grid-template-columns: 44px var(--pipeline-name-width) var(--pipeline-services-width) 112px var(--pipeline-version-width) 72px minmax(140px, 1fr) var(--pipeline-actions-width);
  align-items: center;
  gap: 0;
  min-height: 64px;
  padding: 0 16px 0 14px;
  border: 0;
  border-radius: 0;
  background: linear-gradient(180deg, #151e29 0%, #111923 100%);
}
.icon-btn,
.primary-action,
.text-action,
.more-action {
  height: 31px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: #121923;
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.icon-btn {
  display: inline-grid;
  place-items: center;
  width: 36px;
  padding: 0;
  line-height: 1;
}
.icon-btn svg,
.more-action svg {
  width: 16px;
  height: 16px;
}
.primary-action {
  width: 54px;
  min-width: 54px;
  padding: 0;
  background: linear-gradient(180deg, #2385ff 0%, #1669e3 100%);
  border-color: transparent;
  color: #fff;
  font-weight: 600;
  white-space: nowrap;
}
.text-action {
  width: 34px;
  background: transparent;
  border-color: transparent;
  font-weight: 600;
  white-space: nowrap;
}
.more-action {
  width: 20px;
  border-color: transparent;
  background: transparent;
  color: var(--text-tertiary);
}
.running-badge {
  height: 30px;
  border: 1px solid color-mix(in srgb, var(--accent) 50%, transparent);
  border-radius: 6px;
  background: var(--accent);
  color: #fff;
  cursor: pointer;
  font-size: 12px;
  font-weight: 700;
}
.pipeline-main {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.pipeline-state-dot {
  width: 9px;
  height: 9px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--text-tertiary);
}
.pipeline-state-dot.status-success {
  background: var(--status-success);
}
.pipeline-state-dot.status-running,
.pipeline-state-dot.status-pending {
  background: var(--status-starting);
}
.pipeline-state-dot.status-failed,
.pipeline-state-dot.status-canceled {
  background: var(--status-failed);
}
.pipeline-name-group {
  min-width: 0;
}
.pipeline-name {
  overflow: hidden;
  font-size: 13px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pipeline-services {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}
.service-tag {
  border: 1px solid var(--border-secondary);
  border-radius: 4px;
  max-width: 220px;
  padding: 5px 10px;
  overflow: hidden;
  background: #151e29;
  color: var(--text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.service-tag.muted {
  color: var(--text-tertiary);
}
.pipeline-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  justify-self: start;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  min-width: 62px;
  height: 30px;
  padding: 0 8px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}
.status-icon {
  width: 14px;
  height: 14px;
}
.pipeline-status.status-success {
  border-color: rgba(71, 215, 100, 0.22);
  background: rgba(33, 143, 61, 0.16);
  color: #47d764;
}
.pipeline-status.status-running,
.pipeline-status.status-pending {
  border-color: rgba(255, 189, 23, 0.28);
  background: rgba(210, 153, 19, 0.2);
  color: #ffbd17;
}
.pipeline-status.status-failed,
.pipeline-status.status-canceled {
  border-color: rgba(255, 75, 85, 0.22);
  background: rgba(223, 54, 64, 0.16);
  color: #ff4b55;
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
.pipeline-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
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
