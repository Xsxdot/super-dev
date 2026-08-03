<!--
节点卡片组件

职责：
  - 展示单个远端节点的连通性、Agent 状态和 remote deployment 指标
  - 将指标缺失显示为短横线，避免实时状态缺帧影响布局
  - 点击服务行时向上请求打开 deployment 日志
  - 开发机节点卡展示端口镜像区（逐端口本机⇄远端状态），冲突行点击时向上 emit，
    不在本组件内处理冲突（Task 11）

边界：
  - 不读取 Pinia store——镜像行数据（node.mirrors）和开发机标记（node.devMachine）
    完全经 props 传入，冲突详情弹窗由父级消费 mirror-conflict-click 事件后自行打开
  - 不合并 host 与 node 快照
  - 不打开 workspace tab
-->
<script setup lang="ts">
import type { NodeCenterDeployment, NodeCenterNode } from '@/lib/nodeCenter'
import type { MirrorRowView } from '@/lib/portMirrorView'
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
const emit = defineEmits<{
  'open-logs': [deploymentId: string, nodeId: string]
  // 冲突行被点击时 emit，携带 hostId/port；本组件不判定/不处理冲突，弹窗由父级
  // （NodeCenterView）消费该事件后打开，见文件头边界注释。
  'mirror-conflict-click': [payload: { hostId: string; port: number }]
}>()
const { t } = useAppI18n()

const emptyMetric = '—'

function serviceSummary(count: number): string {
  return t('nodeCenter.serviceCount', { count })
}

function agentSummary(): string {
  const version = props.node.agent.version
    ? t('nodeCenter.agentVersion', { version: props.node.agent.version })
    : t('nodeCenter.agentUnknown')
  const base = `${t('common.remote')} · ${version} · ${props.node.agent.health} · ${serviceSummary(props.node.serviceCount)}`
  // 开发机标记只是摘要行末尾追加的一个词，不影响前面版本/健康度/服务数的既有顺序。
  return props.node.devMachine ? `${base} · ${t('nodeCenter.devMachine')}` : base
}

// showMirrorSection：镜像区是开发机卡的专属呈现，且没有数据时不展示空标题——两个条件
// 都必须满足，即便调用方（理论上）只传了 mirrors 没传 devMachine，也不会误显示。
function showMirrorSection(): boolean {
  return props.node.devMachine && props.node.mirrors.length > 0
}

// mirrorRowText 是结构性文本（IP/端口/符号），不含自然语言词汇，故不经 i18n——
// 与 BottomBar.vue 的 chip 文本、portMirrorView.ts 的 label 字段是同一约定。
// 注意方向与 MirrorRowView.label 相反：label 是"远端端口 ⇄ 本机地址"（服务行视角，
// 主语是远端服务），这里是"本机地址 ⇄ 远端端口"（节点卡视角，主语是本机，对应原型
// node-center.html 的 "本机 ⇄ <host>" 标题方向），两处呈现语境不同，不能共用同一字符串。
function mirrorRowText(row: MirrorRowView): string {
  return `127.0.0.1:${row.port} ⇄ :${row.port}`
}

// mirrorStateLabel 把镜像行状态映射为可译文案。prototype 只画了 active/conflict 两个
// 示例态，但 MirrorRowView.state 实际有 4 态（pending/failed 是真实可能出现的中间态/
// 终态，见 portMirrorView.ts 的 mirrorRowsForHost 不会把它们过滤掉）——这里补全另外两态，
// 避免真实运行时出现空白文案。
function mirrorStateLabel(row: MirrorRowView): string {
  if (row.state === 'active') return t('nodeCenter.mirror.active')
  if (row.state === 'conflict') return t('nodeCenter.mirror.conflict')
  if (row.state === 'pending') return t('nodeCenter.mirror.pending')
  return t('nodeCenter.mirror.failed')
}

function onMirrorRowClick(row: MirrorRowView) {
  if (!row.conflict) return
  emit('mirror-conflict-click', { hostId: row.hostId, port: row.port })
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
            <span
              v-if="node.desktopOnline"
              class="node-route-badge desktop-online"
              data-test="node-desktop-online-badge"
            >{{ t('nodeCenter.desktopOnline') }}</span>
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

    <!-- 端口镜像区：开发机卡专属，服务列表之后（Task 11）。本机节点卡不进 nodes 列表
         （nodeCenter.ts 的 remote-only 过滤），因此这里天然不会出现"本机⇄本机"的行。 -->
    <div v-if="showMirrorSection()" class="mirror-section" data-test="node-mirror-section">
      <h3>{{ t('nodeCenter.mirror.title', { host: node.name }) }}</h3>
      <div
        v-for="row in node.mirrors"
        :key="row.port"
        class="mirror-row"
        :class="{ 'is-conflict': row.conflict }"
        :data-test="`node-mirror-row-${row.port}`"
        :role="row.conflict ? 'button' : undefined"
        :tabindex="row.conflict ? 0 : undefined"
        @click="onMirrorRowClick(row)"
        @keydown.enter.prevent="onMirrorRowClick(row)"
        @keydown.space.prevent="onMirrorRowClick(row)"
      >
        <span class="mirror-row-text">{{ mirrorRowText(row) }}</span>
        <span class="mirror-state" :class="row.state">{{ mirrorStateLabel(row) }}</span>
      </div>
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
/* 桌面端在线徽标（Task 10）：复用 .node-route-badge 胶囊外观，配色沿用原型
   shared/styles.css 的 .node-route-badge.desktop-online（绿色系，语义同
   --status-running），不引入新的裸 hex 值。 */
.node-route-badge.desktop-online {
  border-color: rgba(63, 185, 80, 0.4);
  color: var(--status-running);
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
/* 端口镜像区（Task 11）。颜色沿用项目已有的 --status-* 语义变量，与 EnvGroup.vue 的
   镜像呈现（Task 10）、本文件其余状态色是同一套约定，不引入新的裸 hex 值。 */
.mirror-section {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--border-secondary);
}
.mirror-section h3 {
  margin: 0 0 8px;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  text-overflow: ellipsis;
  /* 故意不用 text-transform: uppercase——标题里插值了 node.name（主机名），uppercase
     会把它一起转成大写（如 "ali-01" 变 "ALI-01"），与卡片标题 <h2>{{ node.name }}</h2>
     的原样大小写呈现不一致。原型 shared/styles.css 的 .mirror-section h3 同样没有用
     uppercase，靠 letter-spacing 做小标题观感。 */
  letter-spacing: 0.04em;
  white-space: nowrap;
}
.mirror-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 26px;
  padding: 3px 0;
  color: var(--text-secondary);
  font-size: 11px;
}
.mirror-row.is-conflict {
  cursor: pointer;
}
.mirror-row.is-conflict:hover .mirror-row-text {
  color: var(--text-primary);
}
.mirror-row-text {
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mirror-state {
  flex-shrink: 0;
  font-weight: 700;
  white-space: nowrap;
}
.mirror-state.active {
  color: var(--status-running);
}
.mirror-state.conflict {
  /* 原型 shared/styles.css 的 .mirror-state.conflict 用 --status-failed（红），与
     EnvGroup.vue 服务行 meta 里更轻量的 .meta-warn（--status-warning/黄，Task 10）
     是两处不同呈现位置各自的既有选色，这里对齐节点卡自己的原型参照，不是不一致。 */
  color: var(--status-failed);
}
.mirror-state.pending {
  color: var(--text-tertiary);
}
.mirror-state.failed {
  color: var(--status-failed);
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
