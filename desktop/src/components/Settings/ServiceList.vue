<!--
ServiceList：当前 env 下的服务列表 + 新增服务入口。

职责：
  - 渲染每个 service 的 ServiceCard
  - 服务草稿变更 / 删除 / 新增向上 emit
边界：
  - 不持有草稿，纯受控组件
-->
<script setup lang="ts">
import type { ConfigDraftService } from '@/lib/configDraft'
import ServiceCard from './ServiceCard.vue'

defineProps<{
  services: ConfigDraftService[]
  envName: string
  hosts: Array<{ id: string; name: string }>
}>()
const emit = defineEmits<{
  'update-service': [number, ConfigDraftService]
  'remove-service': [number]
  'add-service': []
}>()
</script>

<template>
  <div class="service-list">
    <ServiceCard
      v-for="(svc, i) in services" :key="svc.id || i"
      :service="svc" :env-name="envName" :hosts="hosts"
      @update:service="emit('update-service', i, $event)"
      @remove="emit('remove-service', i)"
    />
    <button type="button" class="add-service" data-test="add-service" @click="emit('add-service')">
      + 新增服务
    </button>
  </div>
</template>

<style scoped>
.add-service {
  padding: 6px 12px;
  font-size: 12px;
  background: var(--bg-overlay);
  border: 1px dashed var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
  width: 100%;
}
</style>
