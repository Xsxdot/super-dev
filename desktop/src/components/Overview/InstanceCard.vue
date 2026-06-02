<!--
InstanceCard：展示单个 service×node 实例的进程级指标。

职责：
  - 渲染健康状态、CPU、内存、运行时长、重启次数和运行基座
  - 将未知指标显示为 --
  - 点击时请求打开该实例日志

边界：
  - 不拉取指标
  - 不计算跨节点聚合
-->
<script setup lang="ts">
import type { RuntimeInstanceStatus } from '@/api/agent'

const props = defineProps<{ instance: RuntimeInstanceStatus }>()
const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()

function formatPercent(value: number | null) {
  return value == null ? '--' : `${value.toFixed(1)}%`
}

function formatBytes(value: number | null) {
  if (value == null) return '--'
  if (value >= 1024 * 1024 * 1024) return `${(value / 1024 / 1024 / 1024).toFixed(1)} GiB`
  return `${Math.round(value / 1024 / 1024)} MiB`
}

function formatUptime(value: number | null) {
  if (value == null) return '--'
  const hours = Math.floor(value / 3600)
  const minutes = Math.floor((value % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

function formatRestarts(value: number | null) {
  return value == null ? '--' : String(value)
}

function openLogs() {
  emit('open-logs', props.instance.deployment_id, props.instance.node_id)
}
</script>

<template>
  <button type="button" class="instance-card" :class="`health-${instance.metrics.health}`" @click="openLogs">
    <div class="instance-main">
      <div class="instance-title">{{ instance.service_name }}</div>
      <div class="instance-meta">{{ instance.node_name }} · {{ instance.metrics.base }}</div>
    </div>
    <div class="health-badge">{{ instance.metrics.health }}</div>
    <div class="metric">
      <span>CPU</span>
      <strong>{{ formatPercent(instance.metrics.cpu_percent) }}</strong>
    </div>
    <div class="metric">
      <span>MEM</span>
      <strong>{{ formatBytes(instance.metrics.mem_bytes) }}</strong>
    </div>
    <div class="metric">
      <span>UP</span>
      <strong>{{ formatUptime(instance.metrics.uptime_sec) }}</strong>
    </div>
    <div class="metric">
      <span>RE</span>
      <strong>{{ formatRestarts(instance.metrics.restarts) }}</strong>
    </div>
    <div v-if="instance.error" class="instance-error">{{ instance.error }}</div>
  </button>
</template>

<style scoped>
.instance-card {
  display: grid;
  grid-template-columns: minmax(140px, 1fr) 96px repeat(4, 72px);
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 58px;
  padding: 10px 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  text-align: left;
}
.instance-card:hover {
  border-color: var(--border);
  background: var(--bg-overlay);
}
.instance-main {
  min-width: 0;
}
.instance-title {
  overflow: hidden;
  font-size: 13px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.instance-meta {
  overflow: hidden;
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.health-badge {
  min-width: 0;
  color: var(--status-running);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.metric {
  width: 72px;
  min-width: 72px;
}
.metric span {
  display: block;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
}
.metric strong {
  display: block;
  margin-top: 2px;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 700;
}
.instance-error {
  grid-column: 1 / -1;
  color: var(--status-failed);
  font-size: 11px;
}
.health-failed,
.health-unknown,
.health-restarting,
.health-stopped {
  border-color: rgba(248, 81, 73, 0.5);
}
.health-failed .health-badge,
.health-unknown .health-badge,
.health-restarting .health-badge,
.health-stopped .health-badge {
  color: var(--status-failed);
}
@media (max-width: 760px) {
  .instance-card {
    grid-template-columns: 1fr 92px;
  }
  .metric {
    width: auto;
    min-width: 0;
  }
}
</style>
