<!--
RuntimeStatusTab：项目概览页的运行状态视图。

职责：
  - 从 project 配置与 NodeRegistry 快照投影远端运行态
  - 对本地 deployment 保留 runtime-status 显式刷新 fallback
  - 按用户选择的一级/二级维度透视分组渲染实例卡
  - 网络抖动时显示更新失败角标但保留旧数据

边界：
  - 不保存指标历史
  - 不展示主机整机负载
-->
<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRuntimeStatusStore } from '@/stores/runtimeStatus'
import { useNodeStore } from '@/stores/node'
import { useSettingsStore } from '@/stores/settings'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Deployment, RuntimeInstanceStatus, RuntimeStatusResponse, Service, Project } from '@/api/agent'
import { pivotInstances, type Dimension } from '@/lib/runtimePivot'
import InstanceCard from './InstanceCard.vue'
import PivotToolbar from './PivotToolbar.vue'

const props = defineProps<{ project: Project; active: boolean }>()
const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()
const store = useRuntimeStatusStore()
const nodeStore = useNodeStore()
const settings = useSettingsStore()
const { t } = useAppI18n()

const instances = computed(() => projectRuntimeInstances(props.project, store.statusByProject[props.project.id]))
const groups = computed(() =>
  pivotInstances(instances.value, settings.overviewGrouping.primary, settings.overviewGrouping.secondary),
)
const error = computed(() => store.errorByProject[props.project.id])

function abnormalCount(instances: Array<{ metrics: { health: string } }>) {
  return instances.filter(i => ['failed', 'unknown', 'restarting', 'stopped'].includes(i.metrics.health)).length
}

function groupInstances(group: { children: { instances: RuntimeInstanceStatus[] }[] }) {
  return group.children.flatMap(child => child.instances)
}

function refreshIfActive() {
  if (props.active) void store.refresh(props.project.id)
}

onMounted(refreshIfActive)
watch(() => props.active, active => {
  if (active) void store.refresh(props.project.id)
})
watch(() => props.project.id, () => {
  refreshIfActive()
})

function projectRuntimeInstances(project: Project, fallback?: RuntimeStatusResponse): RuntimeInstanceStatus[] {
  const envOrder = new Map((project.environments ?? []).map((env, idx) => [env.name, env.order || idx + 1]))
  const rows: Array<{ envRank: number; svcOrder: number; instance: RuntimeInstanceStatus }> = []
  for (const service of [...project.services].sort((a, b) => a.order - b.order)) {
    for (const deployment of service.deployments ?? []) {
      const envName = deployment.env_name || 'default'
      const envRank = envOrder.get(envName) ?? 9999
      if (deployment.location === 'remote') {
        for (const hostId of deployment.host_ids ?? []) {
          rows.push({ envRank, svcOrder: service.order, instance: remoteInstance(service, deployment, hostId, envName) })
        }
        continue
      }
      rows.push({ envRank, svcOrder: service.order, instance: localInstance(service, deployment, envName, fallback) })
    }
  }
  // 先按环境序、再按服务序稳定排序; pivot 分组时会保持这个相对顺序。
  rows.sort((a, b) => a.envRank - b.envRank || a.svcOrder - b.svcOrder)
  return rows.map(row => row.instance)
}

function localInstance(service: Service, deployment: Deployment, envName: string, fallback?: RuntimeStatusResponse): RuntimeInstanceStatus {
  const found = findFallbackInstance(fallback, deployment.id)
  if (found) {
    return { ...found, service_id: service.id, service_name: service.name, env_name: envName, deployment_id: deployment.id, is_local: true }
  }
  return unknownInstance(service, deployment, envName, 'local', 'local', true, 'runtime not reported')
}

function remoteInstance(service: Service, deployment: Deployment, hostId: string, envName: string): RuntimeInstanceStatus {
  const node = nodeStore.nodeOf(hostId)
  const nodeName = node?.name || hostId
  if (!node) return unknownInstance(service, deployment, envName, hostId, nodeName, false, 'node not reported')
  if (!node.reachable) {
    return unknownInstance(service, deployment, envName, hostId, nodeName, false, nodeStore.nodeErrorOf(hostId) ?? 'node unreachable')
  }
  const found = Array.isArray(node.deployments)
    ? node.deployments.find(instance => instance.deployment_id === deployment.id)
    : undefined
  if (!found) return unknownInstance(service, deployment, envName, hostId, nodeName, false, 'deployment_not_reported')
  return {
    ...found,
    service_id: service.id,
    service_name: service.name,
    env_name: envName,
    deployment_id: deployment.id,
    node_id: hostId,
    node_name: nodeName,
    is_local: false,
  }
}

function findFallbackInstance(fallback: RuntimeStatusResponse | undefined, deploymentId: string): RuntimeInstanceStatus | undefined {
  for (const env of fallback?.environments ?? []) {
    const found = env.instances.find(instance => instance.deployment_id === deploymentId)
    if (found) return found
  }
  return undefined
}

function unknownInstance(service: Service, deployment: Deployment, envName: string, nodeId: string, nodeName: string, isLocal: boolean, error: string): RuntimeInstanceStatus {
  return {
    service_id: service.id,
    service_name: service.name,
    env_name: envName,
    deployment_id: deployment.id,
    node_id: nodeId,
    node_name: nodeName,
    is_local: isLocal,
    error,
    metrics: {
      cpu_percent: null,
      mem_bytes: null,
      uptime_sec: null,
      restarts: null,
      health: 'unknown',
      base: deployment.runtime?.type ?? 'unknown',
    },
  }
}
</script>

<template>
  <section class="runtime-status">
    <PivotToolbar
      :primary="settings.overviewGrouping.primary"
      :secondary="settings.overviewGrouping.secondary"
      @change="(p: Dimension, s: Dimension) => settings.setOverviewGrouping(p, s)"
    />
    <div v-if="error" class="status-error">{{ t('overview.runtimeStatus.updateFailed') }} · {{ error }}</div>
    <div v-for="group in groups" :key="group.key" class="env-section">
      <header class="env-head">
        <h2>{{ group.label }}</h2>
        <span>{{ t('overview.runtimeStatus.instancesSummary', { count: groupInstances(group).length, abnormal: abnormalCount(groupInstances(group)) }) }}</span>
      </header>
      <div v-for="sub in group.children" :key="sub.key" class="sub-section">
        <div class="sub-label">{{ sub.label }}</div>
        <div class="instance-list">
          <InstanceCard
            v-for="instance in sub.instances"
            :key="`${instance.deployment_id}:${instance.node_id}`"
            :instance="instance"
            @open-logs="(deploymentId, nodeId) => emit('open-logs', deploymentId, nodeId)"
          />
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.runtime-status {
  height: calc(100vh - 65px);
  overflow: auto;
  padding: 16px 20px 28px;
}
.status-error {
  margin-bottom: 12px;
  color: var(--status-failed);
  font-size: 12px;
  font-weight: 700;
}
.env-section + .env-section {
  margin-top: 22px;
}
.env-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}
.env-head h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 700;
}
.env-head span {
  color: var(--text-tertiary);
  font-size: 12px;
}
.sub-section + .sub-section {
  margin-top: 10px;
}
.sub-label {
  margin: 6px 0 4px;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.instance-list {
  display: grid;
  gap: 8px;
}
</style>
