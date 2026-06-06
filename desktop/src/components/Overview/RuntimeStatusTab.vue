<!--
RuntimeStatusTab：项目概览页的运行状态视图。

职责：
  - 从 project 配置与 NodeRegistry 快照投影远端运行态
  - 对本地 deployment 保留 runtime-status 显式刷新 fallback
  - 按环境分段渲染实例卡
  - 网络抖动时显示更新失败角标但保留旧数据

边界：
  - 不保存指标历史
  - 不展示主机整机负载
-->
<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRuntimeStatusStore } from '@/stores/runtimeStatus'
import { useNodeStore } from '@/stores/node'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Deployment, RuntimeInstanceStatus, RuntimeStatusResponse, Service, Project } from '@/api/agent'
import InstanceCard from './InstanceCard.vue'

const props = defineProps<{ project: Project; active: boolean }>()
const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()
const store = useRuntimeStatusStore()
const nodeStore = useNodeStore()
const { t } = useAppI18n()

const status = computed(() => projectRuntimeStatus(props.project, store.statusByProject[props.project.id]))
const error = computed(() => store.errorByProject[props.project.id])

function abnormalCount(instances: Array<{ metrics: { health: string } }>) {
  return instances.filter(i => ['failed', 'unknown', 'restarting', 'stopped'].includes(i.metrics.health)).length
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

function projectRuntimeStatus(project: Project, fallback?: RuntimeStatusResponse): RuntimeStatusResponse {
  const sections = new Map<string, RuntimeInstanceStatus[]>()
  for (const service of [...project.services].sort((a, b) => a.order - b.order)) {
    for (const deployment of service.deployments ?? []) {
      const envName = deployment.env_name || 'default'
      if (!sections.has(envName)) sections.set(envName, [])
      if (deployment.location === 'remote') {
        for (const hostId of deployment.host_ids ?? []) {
          sections.get(envName)!.push(remoteInstance(service, deployment, hostId))
        }
        continue
      }
      sections.get(envName)!.push(localInstance(service, deployment, fallback))
    }
  }
  const envOrder = new Map((project.environments ?? []).map((env, idx) => [env.name, env.order || idx + 1]))
  return {
    environments: [...sections.entries()]
      .sort(([left], [right]) => (envOrder.get(left) ?? 9999) - (envOrder.get(right) ?? 9999) || left.localeCompare(right))
      .map(([env_name, instances]) => ({ env_name, instances })),
  }
}

function localInstance(service: Service, deployment: Deployment, fallback?: RuntimeStatusResponse): RuntimeInstanceStatus {
  const found = findFallbackInstance(fallback, deployment.id)
  if (found) {
    return { ...found, service_id: service.id, service_name: service.name, deployment_id: deployment.id, is_local: true }
  }
  return unknownInstance(service, deployment, 'local', 'local', true, 'runtime not reported')
}

function remoteInstance(service: Service, deployment: Deployment, hostId: string): RuntimeInstanceStatus {
  const node = nodeStore.nodeOf(hostId)
  const nodeName = node?.name || hostId
  if (!node) return unknownInstance(service, deployment, hostId, nodeName, false, 'node not reported')
  if (!node.reachable) {
    return unknownInstance(service, deployment, hostId, nodeName, false, nodeStore.nodeErrorOf(hostId) ?? 'node unreachable')
  }
  const found = Array.isArray(node.deployments)
    ? node.deployments.find(instance => instance.deployment_id === deployment.id)
    : undefined
  if (!found) return unknownInstance(service, deployment, hostId, nodeName, false, 'deployment_not_reported')
  return {
    ...found,
    service_id: service.id,
    service_name: service.name,
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

function unknownInstance(service: Service, deployment: Deployment, nodeId: string, nodeName: string, isLocal: boolean, error: string): RuntimeInstanceStatus {
  return {
    service_id: service.id,
    service_name: service.name,
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
    <div v-if="error" class="status-error">{{ t('overview.runtimeStatus.updateFailed') }} · {{ error }}</div>
    <div v-for="env in status?.environments ?? []" :key="env.env_name" class="env-section">
      <header class="env-head">
        <h2>{{ env.env_name }}</h2>
        <span>{{ t('overview.runtimeStatus.instancesSummary', { count: env.instances.length, abnormal: abnormalCount(env.instances) }) }}</span>
      </header>
      <div class="instance-list">
        <InstanceCard
          v-for="instance in env.instances"
          :key="`${instance.deployment_id}:${instance.node_id}`"
          :instance="instance"
          @open-logs="(deploymentId, nodeId) => emit('open-logs', deploymentId, nodeId)"
        />
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
.instance-list {
  display: grid;
  gap: 8px;
}
</style>
