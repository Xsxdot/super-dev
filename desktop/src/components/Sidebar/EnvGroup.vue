<!--
EnvGroup：侧边栏 Environment 分组。

职责：
  - 展示一个环境名称作为可折叠的分组标题
  - 列出该环境下有 deployment 的 service 行
  - 点击 service 行 emit open-deployment

边界：
  - 不管理折叠以外的任何状态，服务列表由父组件传入
  - 不直接操作 panel store，通过 emit 交给父组件
-->

<script setup lang="ts">
import { ref } from 'vue'
import type { Service } from '@/api/agent'

const props = defineProps<{
  envName: string
  isDev: boolean
  projectId: string
  services: Service[]
  selectedServiceIds: Set<string>
}>()

const emit = defineEmits<{
  'open-deployment': [payload: { deploymentId: string; title: string }]
}>()

// dev 环境默认展开，其他环境默认折叠
const expanded = ref(props.isDev)

function toggleExpanded() {
  expanded.value = !expanded.value
}

/**
 * statusColor 根据 deployment 状态返回对应的颜色值。
 *
 * 参数：
 *   - status: deployment 的状态字符串
 *
 * 返回：
 *   - 对应状态的颜色十六进制字符串
 */
function statusColor(status: string): string {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}

/**
 * onServiceRowClick 处理 service 行点击事件。
 * 找到该 service 在当前环境下的 deployment，emit open-deployment。
 *
 * 参数：
 *   - svc: 被点击的 Service 对象
 */
function onServiceRowClick(svc: Service) {
  const dep = svc.deployments?.find(d => d.env_name === props.envName)
  if (!dep) return
  emit('open-deployment', {
    deploymentId: dep.id,
    title: `${svc.name} · ${props.envName}`,
  })
}
</script>

<template>
  <div class="env-group">
    <!-- 分组标题行，点击切换折叠/展开 -->
    <div
      class="env-group-header"
      data-test="env-group-header"
      @click="toggleExpanded"
    >
      <span class="expand-arrow">{{ expanded ? '▾' : '▸' }}</span>
      <span class="env-name">{{ envName }}</span>
    </div>

    <!-- 展开后的 service 行列表 -->
    <div v-if="expanded" class="env-group-rows" data-test="env-group-rows">
      <div
        v-for="svc in services"
        :key="svc.id"
        class="env-service-row"
        data-test="env-service-row"
        :class="{ selected: selectedServiceIds.has(svc.id) }"
        @click="onServiceRowClick(svc)"
      >
        <span
          class="status-dot"
          :style="{
            background: statusColor(
              svc.deployments?.find(d => d.env_name === envName)?.status ?? ''
            ),
          }"
        />
        <span class="service-name">{{ svc.name }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.env-group {
  margin-bottom: 2px;
}

.env-group-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px 3px 10px;
  border-radius: 4px;
  margin: 1px 4px;
  cursor: pointer;
  transition: background 0.12s;
}

.env-group-header:hover {
  background: rgba(255, 255, 255, 0.04);
}

.expand-arrow {
  font-size: 10px;
  color: var(--text-secondary, #6e7681);
  flex-shrink: 0;
}

.env-name {
  font-size: 11px;
  font-weight: 500;
  color: var(--text-secondary, #6e7681);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.env-group-rows {
  margin: 0 4px 2px 4px;
}

.env-service-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px 3px 20px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
  color: var(--text-primary, #e6edf3);
  transition: background 0.12s;
}

.env-service-row:hover {
  background: rgba(255, 255, 255, 0.04);
}

.env-service-row.selected {
  background: rgba(31, 111, 235, 0.12);
}

.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.service-name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
