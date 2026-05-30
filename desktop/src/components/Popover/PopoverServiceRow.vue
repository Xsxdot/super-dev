<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Service } from '@/api/agent'

const props = defineProps<{
  service: Service
  projectId: string
}>()

const agentStore = useAgentStore()
const hovered = ref(false)

// 托盘 Popover 无 env 选择，统一作用于项目的开发环境。
const envName = computed(() => agentStore.devEnvName(props.projectId))
const devDeployment = computed(() =>
  agentStore.deploymentForServiceInEnv(props.service.id, envName.value),
)

const isActive = computed(() =>
  props.service.status === 'running' || props.service.status === 'starting'
)

const statusColor = computed(() => {
  if (props.service.status === 'running') return '#3fb950'
  if (props.service.status === 'starting') return '#d29922'
  if (props.service.status === 'failed') return '#f85149'
  return '#6e7681'
})

const isChecked = computed(() =>
  agentStore.isServiceEnvSelected(props.projectId, envName.value, props.service.name)
)

async function onCheckChange() {
  if (props.service.required) return
  const project = agentStore.projectById(props.projectId)
  if (!project) return
  const current = project.env_selected_service_ids?.[envName.value] ?? []
  const next = isChecked.value
    ? current.filter((n: string) => n !== props.service.name)
    : [...current, props.service.name]
  await agentStore.putEnvSelected(props.projectId, envName.value, next)
}

async function onToggle() {
  const dep = devDeployment.value
  if (!dep) return
  if (isActive.value) {
    await agentStore.stopDeployment(dep.id)
  } else {
    await agentStore.startDeployment(dep.id)
  }
}

async function onRestart() {
  const dep = devDeployment.value
  if (!dep) return
  await agentStore.restartDeployment(dep.id)
}
</script>

<template>
  <div
    class="popover-service-row"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
  >
    <input
      type="checkbox"
      :checked="isChecked"
      :disabled="service.required"
      @click.stop="onCheckChange"
      class="svc-checkbox"
    />
    <span class="status-dot" :style="{ background: statusColor }" />
    <span class="svc-name" :class="{ dimmed: !isActive && service.status !== '' }">
      {{ service.name }}
    </span>
    <span class="status-label" :style="{ color: statusColor }">
      {{ service.status === 'running' ? '运行中' : service.status === 'starting' ? '启动中…' : service.status === 'failed' ? '已退出' : '未启动' }}
    </span>
    <div class="row-actions" v-if="hovered">
      <button v-if="isActive" title="重启" class="btn" @click.stop="onRestart">↺</button>
      <button
        :title="isActive ? '停止' : '启动'"
        class="btn"
        :class="isActive ? 'btn-stop' : 'btn-start'"
        @click.stop="onToggle"
      >
        {{ isActive ? '⏹' : '▶' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.popover-service-row {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 5px 12px;
  position: relative;
}
.popover-service-row:hover { background: rgba(255,255,255,0.04); }

.svc-checkbox {
  width: 12px; height: 12px;
  accent-color: var(--accent);
  flex-shrink: 0;
  cursor: pointer;
}
.svc-checkbox:disabled { opacity: 0.4; cursor: not-allowed; }

.status-dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.svc-name {
  flex: 1;
  font-size: 11px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.svc-name.dimmed { color: var(--text-tertiary); }

.status-label {
  font-size: 9px;
  white-space: nowrap;
  flex-shrink: 0;
}

.row-actions {
  display: flex;
  gap: 3px;
  flex-shrink: 0;
}
.btn {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 3px;
  padding: 1px 5px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
  line-height: 1.4;
}
.btn-stop { background: rgba(248,81,73,0.1); border-color: rgba(248,81,73,0.3); color: #f85149; }
.btn-start { background: rgba(63,185,80,0.1); border-color: rgba(63,185,80,0.3); color: #3fb950; }
</style>
