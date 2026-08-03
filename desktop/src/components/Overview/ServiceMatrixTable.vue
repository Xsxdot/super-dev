<!--
ServiceMatrixTable：项目概览运行状态页的服务矩阵。

职责：
  - 以服务为主行展示生产/远端环境运行态聚合
  - 用节点健康点表达单服务多节点分布
  - 点击服务行通知父组件切换右侧详情
  - 项目归属远端开发机时，给 dev 环境的节点点位 tooltip 追加 `@ <host>`
    标注（Task 12，纯呈现，数据已在 project.home_host_name 上）

边界：
  - 不拉取运行态数据
  - 不打开日志、不执行启停操作
  - 不改变项目概览外层 Tab
  - 不判断项目是否归属远端——归属信息由父组件透传 homeHostName，本组件只按
    节点所属环境是否为 dev 环境（matrix.devEnvironments）决定要不要标注
-->
<script setup lang="ts">
import type { NodeHealthBead, ServiceMatrix, ServiceMatrixRow } from '@/lib/runtimeServiceMatrix'
import { formatBytes, formatPercent } from '@/lib/runtimeMetrics'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{
  matrix: ServiceMatrix
  selectedServiceId: string
  /**
   * homeHostName：项目当前归属的远端主机展示名；归属本机时为空/undefined。
   * 来自 Project.home_host_name，随 project 一起拿到，这里不发起任何新请求。
   */
  homeHostName?: string
}>()

const emit = defineEmits<{ 'select-service': [serviceId: string] }>()
const { t } = useAppI18n()

function pick(row: ServiceMatrixRow) {
  emit('select-service', row.serviceId)
}

// isDevNode 判断某个节点点位是否属于 dev 环境。只有 dev 环境的节点可能来自
// 「项目归属」的远端开发机（端口镜像）；非 dev 环境走独立的 host_ids 声明，
// 与归属语义无关，不应被一并标注。
function isDevNode(node: NodeHealthBead): boolean {
  return props.matrix.devEnvironments.includes(node.envName)
}

// nodeBeadTitle 组装节点点位的 tooltip 文案，归属远端开发机的 dev 节点额外
// 追加 `@ <host>`。
function nodeBeadTitle(node: NodeHealthBead): string {
  const base = `${node.envName} · ${node.nodeName} · ${node.health}`
  if (isDevNode(node) && props.homeHostName) return `${base} @ ${props.homeHostName}`
  return base
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
                  :title="nodeBeadTitle(node)"
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
