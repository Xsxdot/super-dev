<!--
RuntimeStatusTab：项目概览页的运行状态视图。

职责：
  - 从 project 配置与 NodeRegistry 快照投影远端运行态
  - 对本地 deployment 保留 runtime-status 显式刷新 fallback
  - 按用户选择的一级/二级维度透视分组渲染实例卡
  - 网络抖动时显示更新失败角标但保留旧数据
  - 把 project.home_host_name 透传给 ServiceMatrixTable，供其标注归属远端
    开发机的 dev 节点点位（Task 12，纯呈现，不在本组件做任何判断）

边界：
  - 不保存指标历史
  - 不展示主机整机负载
-->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRuntimeStatusStore } from '@/stores/runtimeStatus'
import { useNodeStore } from '@/stores/node'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { Deployment, RuntimeInstanceStatus, RuntimeStatusResponse, Service, Project } from '@/api/agent'
import { buildServiceMatrix } from '@/lib/runtimeServiceMatrix'
import ServiceMatrixTable from './ServiceMatrixTable.vue'
import ServiceDetailPane from './ServiceDetailPane.vue'

const props = defineProps<{ project: Project; active: boolean }>()
const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()
const store = useRuntimeStatusStore()
const nodeStore = useNodeStore()
const { t } = useAppI18n()
const selectedServiceId = ref('')

const instances = computed(() => projectRuntimeInstances(props.project, store.statusByProject[props.project.id]))
const matrix = computed(() => buildServiceMatrix(props.project, instances.value))
const selectedRow = computed(() =>
  matrix.value.rows.find(row => row.serviceId === selectedServiceId.value) ?? matrix.value.rows[0],
)
const error = computed(() => store.errorByProject[props.project.id])

function refreshIfActive() {
  if (props.active) void store.refresh(props.project.id)
}

function selectService(serviceId: string) {
  selectedServiceId.value = serviceId
}

onMounted(refreshIfActive)
watch(() => props.active, active => {
  if (active) void store.refresh(props.project.id)
})
watch(() => props.project.id, () => {
  refreshIfActive()
})
watch(matrix, next => {
  if (next.rows.some(row => row.serviceId === selectedServiceId.value)) return
  selectedServiceId.value = next.preferredServiceId
}, { immediate: true })

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
    <div class="runtime-kpis">
      <div class="kpi critical" data-test="runtime-kpi-critical">
        <span>{{ t('overview.runtimeStatus.critical') }}</span>
        <strong>{{ matrix.kpis.critical }}</strong>
      </div>
      <div class="kpi" data-test="runtime-kpi-services">
        <span>{{ t('overview.runtimeStatus.services') }}</span>
        <strong>{{ matrix.kpis.services }}</strong>
      </div>
      <div class="kpi" data-test="runtime-kpi-instances">
        <span>{{ t('overview.runtimeStatus.instances') }}</span>
        <strong>{{ matrix.kpis.instances }}</strong>
      </div>
      <div
        v-for="env in matrix.kpis.envs"
        :key="env.envName"
        class="kpi"
        :data-test="`runtime-kpi-env-${env.envName}`"
      >
        <span>{{ env.envName }}</span>
        <strong>{{ env.healthy }}/{{ env.total }}</strong>
      </div>
      <div v-if="matrix.devEnvironments.length > 0" class="kpi quiet" data-test="runtime-kpi-local-dev">
        <span>{{ t('overview.runtimeStatus.localDev') }}</span>
        <strong>{{ matrix.localDev.healthy }}/{{ matrix.localDev.total }}</strong>
      </div>
    </div>
    <div v-if="error" class="status-error">{{ t('overview.runtimeStatus.updateFailed') }} · {{ error }}</div>
    <div class="runtime-grid">
      <ServiceMatrixTable
        :matrix="matrix"
        :selected-service-id="selectedServiceId"
        :home-host-name="project.home_host_name"
        @select-service="selectService"
      />
      <ServiceDetailPane
        :row="selectedRow"
        :environments="matrix.environments"
        :dev-environments="matrix.devEnvironments"
        @open-logs="(deploymentId, nodeId) => emit('open-logs', deploymentId, nodeId)"
      />
    </div>
  </section>
</template>

<style scoped>
.runtime-status {
  height: calc(100vh - 65px);
  overflow: auto;
  padding: 14px 18px 28px;
}
.runtime-kpis {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(108px, 1fr));
  gap: 8px;
  margin-bottom: 12px;
}
.kpi {
  min-height: 48px;
  padding: 8px 10px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
}
.kpi span,
.kpi strong {
  display: block;
}
.kpi span {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 800;
  text-transform: uppercase;
}
.kpi strong {
  margin-top: 3px;
  color: var(--text-primary);
  font-size: 17px;
  font-weight: 800;
}
.kpi.critical strong {
  color: var(--status-failed);
}
.kpi.quiet strong {
  color: var(--text-secondary);
}
.status-error {
  margin-bottom: 12px;
  color: var(--status-failed);
  font-size: 12px;
  font-weight: 700;
}
.runtime-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.55fr) minmax(320px, 0.9fr);
  gap: 12px;
  align-items: start;
}
@media (max-width: 980px) {
  .runtime-grid {
    grid-template-columns: 1fr;
  }
}
</style>
