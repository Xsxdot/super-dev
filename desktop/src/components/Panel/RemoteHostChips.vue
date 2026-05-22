<!--
RemoteHostChips：远程日志面板的 Host 筛选条。

职责：
  - 按当前远程分组展示 Host chip
  - 展示 Host 隧道状态和错误提示
  - 将 Host 选中集合变更上抛给 LogPanel

边界：
  - 不订阅或取消订阅远程日志
  - 不直接修改日志列表
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'
import { tagColor } from '@/lib/tagColor'

const props = defineProps<{
  logSourceId: string
  groupKey: string
  selectedHostIds: Set<string>
}>()

const emit = defineEmits<{
  'update:selectedHostIds': [value: Set<string>]
}>()

const remote = useRemoteStore()
const remoteLog = useRemoteLogStore()

const groupHostIds = computed(() => {
  const view = remoteLog.viewOf(props.logSourceId, props.groupKey)
  if (!view) return [] as string[]
  return view.groups.find(group => group.group_key === props.groupKey)?.host_ids ?? []
})

function toggle(hostId: string) {
  const next = new Set(props.selectedHostIds)
  if (next.has(hostId)) next.delete(hostId)
  else next.add(hostId)
  emit('update:selectedHostIds', next)
}

function stateClass(hostId: string): string {
  const tunnel = remote.tunnelOf(hostId)
  if (!tunnel) return 'state-idle'
  return `state-${tunnel.state}`
}

function chipColor(hostId: string): string {
  const host = remote.hostById(hostId)
  const tag = host?.tags[0]
  return tag ? tagColor(tag) : 'var(--bg-secondary)'
}

function hostError(hostId: string): string {
  return remoteLog.errorOf(props.logSourceId, props.groupKey, hostId) ?? ''
}
</script>

<template>
  <div class="chips-bar">
    <button
      v-for="hostId in groupHostIds"
      :key="hostId"
      :class="['chip', stateClass(hostId), { active: selectedHostIds.has(hostId) }]"
      :style="{ borderColor: chipColor(hostId) }"
      :title="hostError(hostId)"
      data-test="remote-host-chip"
      @click="toggle(hostId)"
    >
      <span class="dot" />
      {{ remote.hostById(hostId)?.name ?? hostId }}
    </button>
  </div>
</template>

<style scoped>
.chips-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 4px 8px;
  border-bottom: 1px solid var(--border-secondary);
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  color: var(--text-primary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-secondary);
  border-left-width: 3px;
  cursor: pointer;
  font-size: 11px;
}
.chip.active {
  background: var(--bg-overlay);
}
.dot {
  width: 6px;
  height: 6px;
  background: var(--text-tertiary);
  border-radius: 50%;
}
.state-open .dot {
  background: var(--status-running);
}
.state-connecting .dot {
  background: var(--status-starting);
}
.state-failed .dot {
  background: var(--status-failed);
}
</style>
