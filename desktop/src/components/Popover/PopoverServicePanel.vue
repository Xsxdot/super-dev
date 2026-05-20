<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import type { Project } from '@/api/agent'
import PopoverServiceRow from './PopoverServiceRow.vue'

const props = defineProps<{ project: Project }>()
const agentStore = useAgentStore()

const requiredServices = computed(() =>
  props.project.services.filter(s => s.required)
)
const optionalServices = computed(() =>
  props.project.services.filter(s => !s.required)
)

const runningCount = computed(() =>
  props.project.services.filter(s => s.status === 'running').length
)
const startingCount = computed(() =>
  props.project.services.filter(s => s.status === 'starting').length
)
const stoppedCount = computed(() =>
  props.project.services.filter(s => s.status !== 'running' && s.status !== 'starting').length
)

const selectedNames = computed(() => props.project.selected_service_ids ?? [])

const selectedServices = computed(() =>
  props.project.services.filter(s =>
    agentStore.isServiceSelectedForStart(props.project.id, s.name)
  )
)

const canStartSelected = computed(() =>
  selectedServices.value.some(
    s => s.status !== 'running' && s.status !== 'starting'
  )
)

const allOptionalSelected = computed(() =>
  optionalServices.value.length > 0 &&
  optionalServices.value.every(s => selectedNames.value.includes(s.name))
)
const someSelected = computed(() =>
  optionalServices.value.some(s => selectedNames.value.includes(s.name))
)

async function toggleSelectAll() {
  const requiredNames = requiredServices.value.map(s => s.name)
  if (allOptionalSelected.value) {
    await agentStore.updateSelected(props.project.id, requiredNames)
  } else {
    const all = props.project.services.map(s => s.name)
    await agentStore.updateSelected(props.project.id, all)
  }
}

async function invertSelection() {
  const requiredNames = requiredServices.value.map(s => s.name)
  const optionalNames = optionalServices.value.map(s => s.name)
  const currentOptionalSelected = optionalNames.filter(n => selectedNames.value.includes(n))
  const inverted = optionalNames.filter(n => !currentOptionalSelected.includes(n))
  const next = [...requiredNames, ...inverted]
  await agentStore.updateSelected(props.project.id, next)
}

async function startSelected() {
  await agentStore.startSelected(props.project.id)
}

async function stopAll() {
  const active = props.project.services.filter(
    s => s.status === 'running' || s.status === 'starting'
  )
  await Promise.all(active.map(s => agentStore.stopService(s.id)))
}

async function openMainWindow() {
  const { invoke } = await import('@tauri-apps/api/core')
  await invoke('show_main_window')
}
</script>

<template>
  <div class="service-panel">
    <!-- Header -->
    <div class="panel-header">
      <div class="header-top">
        <span class="proj-name">{{ project.name }}</span>
        <div class="header-actions">
          <button class="btn btn-secondary" @click="stopAll">全停</button>
          <button class="btn btn-primary" :disabled="!canStartSelected" @click="startSelected">▶ 启动选中</button>
        </div>
      </div>
      <div class="status-badges">
        <span v-if="runningCount > 0" class="badge running">● {{ runningCount }} 运行中</span>
        <span v-if="startingCount > 0" class="badge starting">● {{ startingCount }} 启动中</span>
        <span v-if="stoppedCount > 0" class="badge stopped">● {{ stoppedCount }} 停止</span>
      </div>
    </div>

    <div class="divider" />

    <!-- 工具栏 -->
    <div class="toolbar">
      <button class="toolbar-btn" @click="toggleSelectAll">
        <span
          class="checkbox-glyph"
          :class="{ checked: allOptionalSelected, partial: !allOptionalSelected && someSelected }"
        >
          <span v-if="allOptionalSelected">✓</span>
          <span v-else-if="someSelected">—</span>
        </span>
        全选
      </button>
      <span class="toolbar-divider" />
      <button class="toolbar-btn" @click="invertSelection">反选</button>
    </div>

    <div class="toolbar-separator" />

    <!-- 服务列表 -->
    <div class="service-list">
      <template v-if="requiredServices.length > 0">
        <div class="group-label">必须启动</div>
        <PopoverServiceRow
          v-for="svc in requiredServices"
          :key="svc.id"
          :service="svc"
          :projectId="project.id"
        />
      </template>
      <template v-if="optionalServices.length > 0">
        <div class="group-label">可选</div>
        <PopoverServiceRow
          v-for="svc in optionalServices"
          :key="svc.id"
          :service="svc"
          :projectId="project.id"
        />
      </template>
    </div>

    <div class="divider" />

    <!-- Footer -->
    <div class="panel-footer">
      <button class="footer-btn" @click="openMainWindow">
        ≡ 查看日志
      </button>
    </div>
  </div>
</template>

<style scoped>
.service-panel {
  width: 260px;
  display: flex;
  flex-direction: column;
  background: var(--bg-elevated);
  flex-shrink: 0;
}

.panel-header { padding: 9px 12px; }
.header-top { display: flex; align-items: center; justify-content: space-between; margin-bottom: 6px; }
.proj-name { font-size: 13px; font-weight: 600; color: var(--text-primary); }

.header-actions { display: flex; gap: 5px; }
.btn {
  border-radius: 5px;
  padding: 3px 9px;
  font-size: 10px;
  cursor: pointer;
  border: none;
}
.btn-secondary {
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  color: var(--text-secondary);
}
.btn-primary {
  background: var(--accent);
  color: #fff;
  font-weight: 500;
}
.btn-primary:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.status-badges { display: flex; gap: 5px; flex-wrap: wrap; }
.badge {
  font-size: 9px;
  padding: 1px 7px;
  border-radius: 4px;
}
.badge.running { color: #3fb950; background: rgba(63,185,80,0.1); border: 1px solid rgba(63,185,80,0.2); }
.badge.starting { color: #d29922; background: rgba(210,153,34,0.1); border: 1px solid rgba(210,153,34,0.2); }
.badge.stopped { color: var(--text-tertiary); background: rgba(110,118,129,0.1); border: 1px solid rgba(110,118,129,0.2); }

.divider { height: 1px; background: var(--border); flex-shrink: 0; }

.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 12px;
  background: var(--bg-overlay);
}
.toolbar-btn {
  display: flex; align-items: center; gap: 6px;
  background: transparent; border: none;
  font-size: 10px; color: var(--text-secondary);
  cursor: pointer; padding: 0;
}
.toolbar-divider { width: 1px; height: 12px; background: var(--border); }

.checkbox-glyph {
  width: 13px; height: 13px;
  border-radius: 2px;
  border: 1px solid var(--border);
  background: var(--bg-elevated);
  display: inline-flex; align-items: center; justify-content: center;
  font-size: 9px; font-weight: bold;
  color: var(--text-primary);
}
.checkbox-glyph.checked, .checkbox-glyph.partial {
  border-color: var(--accent);
  background: var(--accent);
  color: #fff;
}

.toolbar-separator { height: 1px; background: var(--bg-elevated); flex-shrink: 0; }

.service-list { flex: 1; overflow-y: auto; }

.group-label {
  font-size: 9px; font-weight: 600;
  color: var(--text-tertiary);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  padding: 8px 12px 3px;
}

.panel-footer { padding: 7px 12px; display: flex; justify-content: flex-end; }
.footer-btn {
  background: transparent; border: none;
  font-size: 10px; color: var(--accent);
  cursor: pointer;
}
</style>
