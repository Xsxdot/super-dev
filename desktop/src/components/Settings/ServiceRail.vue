<!--
ServiceRail：左栏服务导航列表。

职责：
  - 渲染服务列表，高亮选中项
  - 展示每个服务在当前 env 的配置状态徽标（本地/远程N台/未配置）
  - 新增/删除/选中 emit 给父层处理
边界：
  - 不持有草稿，纯受控组件
  - 删除只 emit index，不做确认（确认由父层决定）
-->
<script setup lang="ts">
import type { ConfigDraftService } from '@/lib/configDraft'

const props = defineProps<{
  services: ConfigDraftService[]
  activeId: string
  envName: string
}>()
const emit = defineEmits<{
  'select': [id: string]
  'add': []
  'remove': [index: number]
}>()

function badge(svc: ConfigDraftService): string {
  const dep = svc.deployments.find(d => d.env_name === props.envName)
  if (!dep) return '未配置'
  if (dep.location === 'local') return '本地'
  const count = dep.host_ids?.length ?? 0
  return count > 0 ? `远程 ${count} 台` : '远程'
}

function isActive(svc: ConfigDraftService, i: number): boolean {
  if (svc.id) return svc.id === props.activeId
  return String(i) === props.activeId
}
</script>

<template>
  <div class="rail">
    <div
      v-for="(svc, i) in services" :key="svc.id || i"
      class="rail-item" data-test="rail-item"
      :class="{ active: isActive(svc, i) }"
      @click="emit('select', svc.id || String(i))"
    >
      <span class="rail-name">{{ svc.name || '（未命名）' }}</span>
      <span class="rail-badge" :class="badge(svc) === '未配置' ? 'badge-empty' : 'badge-ok'">{{ badge(svc) }}</span>
      <button type="button" class="rail-del" data-test="rail-del" @click.stop="emit('remove', i)">✕</button>
    </div>
    <button type="button" class="rail-add" data-test="rail-add" @click="emit('add')">+ 新增服务</button>
  </div>
</template>

<style scoped>
.rail {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 0;
}
.rail-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 10px;
  cursor: pointer;
  border-radius: 4px;
  font-size: 13px;
  color: var(--text-secondary);
}
.rail-item:hover {
  background: var(--bg-overlay);
}
.rail-item.active {
  background: var(--bg-overlay);
  color: var(--text-primary);
}
.rail-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rail-badge {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  white-space: nowrap;
  flex-shrink: 0;
}
.badge-ok {
  background: color-mix(in srgb, var(--accent) 15%, transparent);
  color: var(--accent);
}
.badge-empty {
  background: var(--bg-secondary);
  color: var(--text-tertiary);
}
.rail-del {
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 10px;
  padding: 1px 4px;
  opacity: 0;
  flex-shrink: 0;
}
.rail-item:hover .rail-del {
  opacity: 1;
}
.rail-add {
  margin-top: 4px;
  padding: 6px 10px;
  font-size: 12px;
  background: transparent;
  border: 1px dashed var(--border-secondary);
  color: var(--text-secondary);
  cursor: pointer;
  border-radius: 4px;
  text-align: left;
}
</style>
