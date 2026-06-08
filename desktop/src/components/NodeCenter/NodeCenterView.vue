<!--
节点中心视图

职责：
  - 展示所有远端节点的实时运行态总览
  - 合并 remoteStore.hosts 与 nodeStore.nodesList，保证未连接主机仍可见
  - 点击远端服务行时复用现有 deployment 日志 tab

边界：
  - 不创建新的日志连接模型
  - 不管理远端服务启停操作
  - 不展示本机 local 服务
-->
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import NodeCard from './NodeCard.vue'
import { buildNodeCenterNodes, isRemoteNodeHost } from '@/lib/nodeCenter'
import { useAgentStore } from '@/stores/agent'
import { useNodeStore } from '@/stores/node'
import { useRemoteStore } from '@/stores/remote'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppI18n } from '@/i18n/useAppI18n'

const agentStore = useAgentStore()
const nodeStore = useNodeStore()
const remoteStore = useRemoteStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()

const remoteHosts = computed(() => remoteStore.hosts.filter(isRemoteNodeHost))
const nodes = computed(() =>
  buildNodeCenterNodes(remoteStore.hosts, nodeStore.nodesList, agentStore.projects),
)
const abnormalCount = computed(() =>
  nodes.value.reduce((sum, node) => sum + node.deployments.filter(item => item.abnormal).length, 0),
)
const connectedLabel = computed(() =>
  nodeStore.connected ? t('nodeCenter.streamConnected') : t('nodeCenter.streamDisconnected'),
)

onMounted(() => {
  if (remoteStore.hosts.length > 0) return
  void remoteStore.loadHosts().catch(() => undefined)
})

function openLogs(deploymentId: string) {
  const info = agentStore.serviceForDeployment(deploymentId)
  workspace.openDeployment(deploymentId, info ? `${info.service.name} · ${info.envName}` : deploymentId)
}
</script>

<template>
  <section class="node-center-view">
    <header class="node-center-head">
      <div class="node-center-title-group">
        <div class="node-center-kicker">{{ t('nodeCenter.kicker') }}</div>
        <h1>{{ t('nodeCenter.title') }}</h1>
      </div>
      <div class="node-center-summary">
        <span>{{ t('nodeCenter.nodeSummary', { count: nodes.length, abnormal: abnormalCount }) }}</span>
        <span class="stream-state">{{ connectedLabel }}</span>
      </div>
    </header>

    <div v-if="remoteHosts.length === 0 && nodes.length === 0" class="node-center-empty" data-test="node-center-empty">
      <h2>{{ t('nodeCenter.emptyTitle') }}</h2>
      <p>{{ t('nodeCenter.emptyDescription') }}</p>
    </div>

    <div v-else class="node-card-grid">
      <NodeCard
        v-for="node in nodes"
        :key="node.hostId"
        :node="node"
        @open-logs="openLogs"
      />
    </div>
  </section>
</template>

<style scoped>
.node-center-view {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.node-center-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 64px;
  padding: 16px 20px 12px;
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
}
.node-center-title-group {
  min-width: 0;
}
.node-center-kicker {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.node-center-head h1 {
  margin: 2px 0 0;
  overflow: hidden;
  font-size: 20px;
  font-weight: 700;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-center-summary {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 700;
}
.stream-state {
  color: var(--text-secondary);
}
.node-card-grid {
  display: grid;
  width: 100%;
  grid-template-columns: repeat(auto-fill, minmax(max(400px, calc((100% - 36px) / 4)), 1fr));
  gap: 12px;
  min-height: 0;
  overflow: auto;
  padding: 16px 20px 28px;
}
.node-center-empty {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 24px;
  color: var(--text-tertiary);
  text-align: center;
}
.node-center-empty h2 {
  margin: 0;
  color: var(--text-primary);
  font-size: 16px;
}
.node-center-empty p {
  max-width: 360px;
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.5;
}
@media (max-width: 760px) {
  .node-center-head {
    align-items: flex-start;
    flex-direction: column;
  }
  .node-center-summary {
    justify-content: flex-start;
  }
  .node-card-grid {
    grid-template-columns: minmax(0, 1fr);
    padding: 12px;
  }
}
</style>
