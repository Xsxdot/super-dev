<!--
节点卡片组件

职责：
  - 展示单个远端节点的连通性、Agent 状态和 remote deployment 指标
  - 将指标缺失显示为短横线，避免实时状态缺帧影响布局
  - 点击服务行时向上请求打开 deployment 日志

边界：
  - 不读取 Pinia store
  - 不合并 host 与 node 快照
  - 不打开 workspace tab
-->
<script setup lang="ts">
import type { NodeCenterDeployment, NodeCenterNode } from '@/lib/nodeCenter'
import {
  cpuBarWidth,
  formatBytes,
  formatPercent,
  formatRestarts,
  formatUptime,
} from '@/lib/runtimeMetrics'
import { transportTypeLabelKey } from '@/lib/agentRoute'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ node: NodeCenterNode }>()
const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()
const { t } = useAppI18n()

const emptyMetric = '—'

function serviceSummary(count: number): string {
  return t('nodeCenter.serviceCount', { count })
}

function agentSummary(): string {
  const version = props.node.agent.version
    ? t('nodeCenter.agentVersion', { version: props.node.agent.version })
    : t('nodeCenter.agentUnknown')
  return `${t('common.remote')} · ${version} · ${props.node.agent.health} · ${serviceSummary(props.node.serviceCount)}`
}

function routeSummary(): string {
  if (!props.node.route?.selectedType) return ''
  const label = t(transportTypeLabelKey(props.node.route.selectedType))
  return props.node.route.degraded ? `${label} · ${t('nodeCenter.degraded')}` : label
}

function serviceContext(deployment: NodeCenterDeployment): string {
  return [deployment.projectName, deployment.envName].filter(Boolean).join(' · ')
}

function openLogs(deploymentId: string, nodeId: string) {
  emit('open-logs', deploymentId, nodeId)
}
</script>

<template>
  <article
    class="node-card"
    :class="{ 'is-reachable': node.reachable, 'is-muted': node.muted }"
    :data-test="`node-card-${node.hostId}`"
  >
    <header class="node-card-head">
      <div class="node-title-group">
        <span class="node-status-dot" aria-hidden="true"></span>
        <div class="node-title-text">
          <div class="node-title-row">
            <h2>{{ node.name }}</h2>
            <span v-if="routeSummary()" class="node-route-badge" data-test="node-route-badge">{{ routeSummary() }}</span>
          </div>
          <p>{{ agentSummary() }}</p>
        </div>
      </div>
      <span class="node-health-badge">{{ node.reachable ? node.agent.health : t('nodeCenter.disconnected') }}</span>
    </header>

    <div v-if="node.error" class="node-error">{{ node.error }}</div>

    <div v-if="node.deployments.length === 0" class="node-empty">
      {{ t('nodeCenter.noRemoteServices') }}
    </div>

    <div v-else class="node-service-list">
      <button
        v-for="deployment in node.deployments"
        :key="`${deployment.instance.deployment_id}:${deployment.instance.node_id}`"
        type="button"
        class="node-service-row"
        :class="{ abnormal: deployment.abnormal }"
        :data-test="`node-service-${deployment.instance.deployment_id}`"
        @click="openLogs(deployment.instance.deployment_id, deployment.instance.node_id)"
      >
        <span class="service-status-dot" :class="`health-${deployment.instance.metrics.health}`" aria-hidden="true"></span>
        <span class="service-main">
          <span class="service-name" data-test="service-name">{{ deployment.instance.service_name }}</span>
          <span v-if="serviceContext(deployment)" class="service-context" data-test="service-context">
            {{ serviceContext(deployment) }}
          </span>
        </span>
        <span class="service-metrics">
          <span class="metric cpu">
            <span class="metric-label">CPU</span>
            <span class="metric-value">{{ formatPercent(deployment.instance.metrics.cpu_percent, emptyMetric) }}</span>
            <span v-if="cpuBarWidth(deployment.instance.metrics.cpu_percent) !== null" class="cpu-bar">
              <span
                class="cpu-bar-fill"
                :data-test="`cpu-bar-${deployment.instance.deployment_id}`"
                :style="{ width: `${cpuBarWidth(deployment.instance.metrics.cpu_percent)}%` }"
              ></span>
            </span>
          </span>
          <span class="metric">
            <span class="metric-label">MEM</span>
            <span class="metric-value">{{ formatBytes(deployment.instance.metrics.mem_bytes, emptyMetric) }}</span>
          </span>
          <span class="metric">
            <span class="metric-label">UP</span>
            <span class="metric-value">{{ formatUptime(deployment.instance.metrics.uptime_sec, emptyMetric) }}</span>
          </span>
          <span class="metric">
            <span class="metric-label">RE</span>
            <span class="metric-value">{{ formatRestarts(deployment.instance.metrics.restarts, emptyMetric) }}</span>
          </span>
        </span>
        <span v-if="deployment.instance.error" class="service-error">{{ deployment.instance.error }}</span>
      </button>
    </div>
  </article>
</template>

<style scoped>
.node-card {
  min-width: 0;
  padding: 14px;
  border: 1px solid var(--border-secondary);
  border-radius: 7px;
  background: var(--bg-elevated);
  color: var(--text-primary);
}
.node-card.is-muted {
  opacity: 0.72;
}
.node-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.node-title-group {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 8px;
}
.node-status-dot,
.service-status-dot {
  width: 8px;
  height: 8px;
  margin-top: 6px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--status-failed);
}
.is-reachable .node-status-dot {
  background: var(--status-running);
}
.node-title-text {
  min-width: 0;
}
.node-title-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}
.node-title-text h2 {
  margin: 0;
  overflow: hidden;
  font-size: 14px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-route-badge {
  max-width: 112px;
  padding: 1px 6px;
  border: 1px solid rgba(210, 153, 34, 0.32);
  border-radius: 999px;
  color: var(--status-warning);
  font-size: 10px;
  font-weight: 700;
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-title-text p {
  margin: 3px 0 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-health-badge {
  flex-shrink: 0;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.node-error,
.service-error {
  color: var(--status-failed);
  font-size: 11px;
}
.node-error {
  margin-top: 10px;
}
.node-empty {
  margin-top: 14px;
  padding: 12px;
  border: 1px dashed var(--border-secondary);
  border-radius: 6px;
  color: var(--text-tertiary);
  font-size: 12px;
}
.node-service-list {
  display: grid;
  gap: 8px;
  margin-top: 14px;
}
.node-service-row {
  display: grid;
  grid-template-columns: 12px minmax(88px, 1fr) minmax(0, 240px);
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 42px;
  padding: 8px 9px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  color: inherit;
  cursor: pointer;
  overflow: hidden;
  text-align: left;
}
.node-service-row:hover {
  border-color: var(--border);
  background: var(--bg-overlay);
}
.node-service-row.abnormal {
  border-color: rgba(248, 81, 73, 0.45);
}
.health-running,
.health-healthy {
  background: var(--status-running);
}
.health-failed,
.health-unknown,
.health-restarting,
.health-stopped {
  background: var(--status-failed);
}
.service-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  align-items: flex-start;
  gap: 2px;
}
.service-name {
  display: block;
  max-width: 100%;
  overflow: hidden;
  font-size: 12px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.service-context {
  display: block;
  max-width: 100%;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 600;
  line-height: 1.25;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.service-metrics {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 76px) minmax(0, 64px) minmax(0, 58px) minmax(0, 36px);
  gap: 8px;
  justify-content: end;
}
.metric {
  min-width: 0;
}
.metric-label {
  display: block;
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 700;
}
.metric-value {
  display: block;
  margin-top: 2px;
  font-size: 11px;
  font-weight: 700;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cpu-bar {
  display: block;
  width: min(54px, 100%);
  height: 3px;
  margin-top: 4px;
  border-radius: 999px;
  background: rgba(139, 148, 158, 0.22);
  overflow: hidden;
}
.cpu-bar-fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--status-running);
}
@media (max-width: 760px) {
  .node-service-row {
    grid-template-columns: 12px minmax(0, 1fr);
  }
  .service-metrics {
    grid-column: 2;
    width: 100%;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    justify-content: stretch;
  }
}
</style>
