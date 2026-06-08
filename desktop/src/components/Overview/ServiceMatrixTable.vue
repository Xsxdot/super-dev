<!--
ServiceMatrixTable：项目概览运行状态页的服务矩阵。

职责：
  - 以服务为主行展示生产/远端环境运行态聚合
  - 用节点健康点表达单服务多节点分布
  - 点击服务行通知父组件切换右侧详情

边界：
  - 不拉取运行态数据
  - 不打开日志、不执行启停操作
  - 不改变项目概览外层 Tab
-->
<script setup lang="ts">
import type { ServiceMatrix, ServiceMatrixRow } from '@/lib/runtimeServiceMatrix'
import { formatBytes, formatPercent } from '@/lib/runtimeMetrics'
import { useAppI18n } from '@/i18n/useAppI18n'

defineProps<{
  matrix: ServiceMatrix
  selectedServiceId: string
}>()

const emit = defineEmits<{ 'select-service': [serviceId: string] }>()
const { t } = useAppI18n()

function pick(row: ServiceMatrixRow) {
  emit('select-service', row.serviceId)
}
</script>

<template>
  <section class="service-matrix">
    <header class="matrix-head">
      <div>
        <h2>{{ t('overview.runtimeStatus.serviceMatrix') }}</h2>
        <span>{{ t('overview.runtimeStatus.serviceMatrixSummary', { services: matrix.rows.length, instances: matrix.kpis.instances }) }}</span>
      </div>
    </header>
    <div class="matrix-scroll">
      <table>
        <thead>
          <tr>
            <th>{{ t('overview.runtimeStatus.service') }}</th>
            <th v-for="env in matrix.environments" :key="env" :data-test="`matrix-env-${env}`">{{ env }}</th>
            <th>{{ t('overview.runtimeStatus.nodes') }}</th>
            <th>{{ t('overview.runtimeStatus.cpu') }}</th>
            <th>{{ t('overview.runtimeStatus.mem') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in matrix.rows"
            :key="row.serviceId"
            :data-test="`service-row-${row.serviceId}`"
            :class="{ selected: row.serviceId === selectedServiceId, abnormal: row.abnormal > 0 }"
            tabindex="0"
            @click="pick(row)"
            @keydown.enter.prevent="pick(row)"
            @keydown.space.prevent="pick(row)"
          >
            <td class="service-cell">
              <strong>{{ row.serviceName }}</strong>
              <span>{{ t('overview.runtimeStatus.rowSummary', { instances: row.total, abnormal: row.abnormal }) }}</span>
            </td>
            <td v-for="cell in row.envs" :key="cell.envName">
              <span class="status-pill" :class="`health-${cell.health}`">{{ cell.label }}</span>
            </td>
            <td>
              <span class="node-beads">
                <span
                  v-for="node in row.nodeHealths"
                  :key="`${node.envName}:${node.nodeId}`"
                  class="node-bead"
                  :class="`health-${node.health}`"
                  :title="`${node.envName} · ${node.nodeName} · ${node.health}`"
                />
              </span>
            </td>
            <td class="metric">{{ formatPercent(row.cpuPercent) }}</td>
            <td class="metric">{{ formatBytes(row.memBytes) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.service-matrix {
  min-width: 0;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
}
.matrix-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 48px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-secondary);
}
.matrix-head h2 {
  margin: 0;
  font-size: 13px;
  font-weight: 700;
}
.matrix-head span {
  color: var(--text-tertiary);
  font-size: 11px;
}
.matrix-scroll {
  overflow: auto;
}
table {
  width: 100%;
  min-width: 620px;
  border-collapse: collapse;
}
th,
td {
  height: 44px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border-secondary);
  text-align: left;
  vertical-align: middle;
}
th {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 800;
  text-transform: uppercase;
}
tbody tr {
  cursor: pointer;
}
tbody tr:hover,
tbody tr.selected {
  background: var(--bg-overlay);
}
tbody tr.selected {
  box-shadow: inset 2px 0 0 var(--accent, #1f6feb);
}
tbody tr:last-child td {
  border-bottom: 0;
}
.service-cell strong,
.service-cell span {
  display: block;
}
.service-cell strong {
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 700;
}
.service-cell span {
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 11px;
}
.status-pill {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 999px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
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
.health-not_configured {
  color: var(--text-tertiary);
}
.node-beads {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.node-bead {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}
.metric {
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}
</style>
