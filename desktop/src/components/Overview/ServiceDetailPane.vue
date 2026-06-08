<!--
ServiceDetailPane：项目概览运行状态页的服务节点详情。

职责：
  - 展示当前选中服务在生产/远端环境下的实例节点表
  - 将 Local Dev 作为辅助区展示，避免与项目态势平权
  - 点击节点行请求父组件打开对应日志

边界：
  - 不选择服务
  - 不拉取运行态数据
  - 不执行服务启停操作
-->
<script setup lang="ts">
import type { RuntimeInstanceStatus } from '@/api/agent'
import type { ServiceMatrixRow } from '@/lib/runtimeServiceMatrix'
import { formatBytes, formatPercent, formatRestarts, formatUptime } from '@/lib/runtimeMetrics'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  row: ServiceMatrixRow | undefined
  environments: string[]
  devEnvironments: string[]
}>()

const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()
const { t } = useAppI18n()

function instancesForEnv(envName: string): RuntimeInstanceStatus[] {
  return props.row?.instances.filter(instance => instance.env_name === envName) ?? []
}

function summaryForEnv(envName: string): string {
  return props.row?.envs.find(env => env.envName === envName)?.label
    ?? props.row?.devEnvs.find(env => env.envName === envName)?.label
    ?? 'Not configured'
}

function openLogs(instance: RuntimeInstanceStatus) {
  emit('open-logs', instance.deployment_id, instance.node_id)
}
</script>

<template>
  <aside class="service-detail">
    <template v-if="row">
      <header class="detail-head">
        <div>
          <span>{{ t('overview.runtimeStatus.selectedService') }}</span>
          <h2>{{ row.serviceName }}</h2>
        </div>
        <strong :class="{ abnormal: row.abnormal > 0 }">{{ t('overview.runtimeStatus.abnormalSummary', { count: row.abnormal }) }}</strong>
      </header>

      <section class="detail-band">
        <div class="band-title">{{ t('overview.runtimeStatus.productionRemote') }}</div>
        <section v-for="env in environments" :key="env" class="env-detail">
          <header>
            <h3>{{ env }}</h3>
            <span>{{ summaryForEnv(env) }}</span>
          </header>
          <div v-if="instancesForEnv(env).length === 0" class="empty-env">{{ t('overview.runtimeStatus.noNodesConfigured') }}</div>
          <table v-else>
            <thead>
              <tr>
                <th>{{ t('overview.runtimeStatus.nodes') }}</th>
                <th>{{ t('overview.runtimeStatus.health') }}</th>
                <th>{{ t('overview.runtimeStatus.cpu') }}</th>
                <th>{{ t('overview.runtimeStatus.mem') }}</th>
                <th>{{ t('overview.runtimeStatus.uptime') }}</th>
                <th>{{ t('overview.runtimeStatus.restarts') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="instance in instancesForEnv(env)"
                :key="`${instance.deployment_id}:${instance.node_id}`"
                :data-test="`node-row-${instance.deployment_id}-${instance.node_id}`"
                tabindex="0"
                @click="openLogs(instance)"
                @keydown.enter.prevent="openLogs(instance)"
                @keydown.space.prevent="openLogs(instance)"
              >
                <td class="node-cell">
                  <strong>{{ instance.node_name }}</strong>
                  <span>{{ instance.metrics.base }}</span>
                </td>
                <td class="health" :class="`health-${instance.metrics.health}`">{{ instance.metrics.health }}</td>
                <td>{{ formatPercent(instance.metrics.cpu_percent) }}</td>
                <td>{{ formatBytes(instance.metrics.mem_bytes) }}</td>
                <td>{{ formatUptime(instance.metrics.uptime_sec) }}</td>
                <td>{{ formatRestarts(instance.metrics.restarts) }}</td>
              </tr>
            </tbody>
          </table>
        </section>
      </section>

      <section v-if="devEnvironments.length > 0" class="detail-band local-dev">
        <div class="band-title">{{ t('overview.runtimeStatus.localDev') }}</div>
        <section v-for="env in devEnvironments" :key="env" class="env-detail">
          <header>
            <h3>{{ env }}</h3>
            <span>{{ summaryForEnv(env) }}</span>
          </header>
          <div v-if="instancesForEnv(env).length === 0" class="empty-env">{{ t('overview.runtimeStatus.noNodesConfigured') }}</div>
          <table v-else>
            <tbody>
              <tr
                v-for="instance in instancesForEnv(env)"
                :key="`${instance.deployment_id}:${instance.node_id}`"
                :data-test="`node-row-${instance.deployment_id}-${instance.node_id}`"
                tabindex="0"
                @click="openLogs(instance)"
                @keydown.enter.prevent="openLogs(instance)"
                @keydown.space.prevent="openLogs(instance)"
              >
                <td class="node-cell">
                  <strong>{{ instance.node_name }}</strong>
                  <span>{{ instance.metrics.base }}</span>
                </td>
                <td class="health" :class="`health-${instance.metrics.health}`">{{ instance.metrics.health }}</td>
                <td>{{ formatPercent(instance.metrics.cpu_percent) }}</td>
                <td>{{ formatBytes(instance.metrics.mem_bytes) }}</td>
                <td>{{ formatUptime(instance.metrics.uptime_sec) }}</td>
                <td>{{ formatRestarts(instance.metrics.restarts) }}</td>
              </tr>
            </tbody>
          </table>
        </section>
      </section>
    </template>
    <div v-else class="detail-empty">{{ t('overview.runtimeStatus.selectServiceHint') }}</div>
  </aside>
</template>

<style scoped>
.service-detail {
  min-width: 0;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
}
.detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 12px;
  border-bottom: 1px solid var(--border-secondary);
}
.detail-head span,
.band-title {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 800;
  text-transform: uppercase;
}
.detail-head h2 {
  margin: 2px 0 0;
  font-size: 16px;
  font-weight: 700;
}
.detail-head strong {
  color: var(--text-tertiary);
  font-size: 12px;
}
.detail-head strong.abnormal {
  color: var(--status-failed);
}
.detail-band {
  padding: 10px 12px 12px;
}
.detail-band + .detail-band {
  border-top: 1px solid var(--border-secondary);
}
.band-title {
  margin-bottom: 8px;
}
.env-detail + .env-detail {
  margin-top: 10px;
}
.env-detail header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
}
.env-detail h3 {
  margin: 0;
  font-size: 11px;
  font-weight: 800;
  text-transform: uppercase;
}
.env-detail header span,
.empty-env,
.detail-empty {
  color: var(--text-tertiary);
  font-size: 11px;
}
table {
  width: 100%;
  border-collapse: collapse;
}
th,
td {
  height: 32px;
  padding: 5px 6px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
  font-size: 11px;
  white-space: nowrap;
}
th {
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 800;
  text-transform: uppercase;
}
tbody tr {
  cursor: pointer;
}
tbody tr:hover {
  background: var(--bg-overlay);
}
tbody tr:last-child td {
  border-bottom: 0;
}
.node-cell strong,
.node-cell span {
  display: block;
}
.node-cell strong {
  color: var(--text-primary);
  font-size: 12px;
}
.node-cell span {
  color: var(--text-tertiary);
  font-size: 10px;
}
.health-running,
.health-healthy {
  color: var(--status-running);
}
.health-stopped,
.health-failed,
.health-restarting,
.health-unknown {
  color: var(--status-failed);
}
.detail-empty {
  display: grid;
  min-height: 220px;
  place-items: center;
  padding: 24px;
}
</style>
