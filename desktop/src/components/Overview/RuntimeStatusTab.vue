<!--
RuntimeStatusTab：项目概览页的运行状态视图。

职责：
  - 管理 runtime-status 轮询生命周期
  - 按环境分段渲染实例卡
  - 网络抖动时显示更新失败角标但保留旧数据

边界：
  - 不保存指标历史
  - 不展示主机整机负载
-->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from 'vue'
import { useRuntimeStatusStore } from '@/stores/runtimeStatus'
import InstanceCard from './InstanceCard.vue'

const props = defineProps<{ projectId: string; active: boolean }>()
const emit = defineEmits<{ 'open-logs': [deploymentId: string, nodeId: string] }>()
const store = useRuntimeStatusStore()

const status = computed(() => store.statusByProject[props.projectId])
const error = computed(() => store.errorByProject[props.projectId])

function abnormalCount(instances: Array<{ metrics: { health: string } }>) {
  return instances.filter(i => ['failed', 'unknown', 'restarting', 'stopped'].includes(i.metrics.health)).length
}

function startIfActive() {
  if (props.active) store.start(props.projectId, 5000)
}

onMounted(startIfActive)
onUnmounted(() => store.stop(props.projectId))
watch(() => props.active, active => {
  if (active) store.start(props.projectId, 5000)
  else store.stop(props.projectId)
})
watch(() => props.projectId, (projectId, oldProjectId) => {
  if (oldProjectId) store.stop(oldProjectId)
  if (props.active) store.start(projectId, 5000)
})
</script>

<template>
  <section class="runtime-status">
    <div v-if="error" class="status-error">Update failed · {{ error }}</div>
    <div v-for="env in status?.environments ?? []" :key="env.env_name" class="env-section">
      <header class="env-head">
        <h2>{{ env.env_name }}</h2>
        <span>{{ env.instances.length }} instances · {{ abnormalCount(env.instances) }} abnormal</span>
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
