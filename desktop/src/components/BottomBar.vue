<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePanelStore } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useBookmarkStore } from '@/stores/bookmark'

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const bookmarkStore = useBookmarkStore()

// 所有面板中的服务（去重）
const panelServices = computed(() => {
  const seen = new Set<string>()
  const result = []
  for (const leaf of panelStore.allLeaves) {
    if (leaf.serviceId && !seen.has(leaf.serviceId)) {
      seen.add(leaf.serviceId)
      const svc = agentStore.serviceById(leaf.serviceId)
      if (svc) result.push(svc)
    }
  }
  return result
})

// 底部栏勾选状态（独立于侧边栏 selected_service_ids）
const checkedIds = ref<Set<string>>(new Set())

function toggleCheck(serviceId: string) {
  if (checkedIds.value.has(serviceId)) {
    checkedIds.value.delete(serviceId)
  } else {
    checkedIds.value.add(serviceId)
  }
  // 强制 Vue 响应性：创建新 Set 以触发更新
  checkedIds.value = new Set(checkedIds.value)
}

async function restartChecked() {
  await Promise.all([...checkedIds.value].map(id => agentStore.restartService(id)))
}

async function stopChecked() {
  await Promise.all([...checkedIds.value].map(id => agentStore.stopService(id)))
}

// 同步录制
const syncEnabled = ref(false)
const syncRecording = computed(() => bookmarkStore.syncRecording)

function toggleSync() {
  syncEnabled.value = !syncEnabled.value
  if (syncEnabled.value) {
    // 把所有面板加入同步组
    for (const leaf of panelStore.allLeaves) {
      if (leaf.serviceId) {
        bookmarkStore.syncPanelIds.value.add(leaf.id)
      }
    }
  } else {
    bookmarkStore.syncPanelIds.value.clear()
  }
}

function toggleSyncRecord() {
  if (syncRecording.value) {
    bookmarkStore.endSyncBookmark()
  } else {
    bookmarkStore.startSyncBookmark()
  }
}

const statusColor = (status: string) => {
  if (status === 'running') return '#3fb950'
  if (status === 'starting') return '#d29922'
  if (status === 'failed') return '#f85149'
  return '#6e7681'
}
</script>

<template>
  <div class="bottom-bar">
    <span class="label">面板服务</span>

    <div class="service-chips">
      <div
        v-for="svc in panelServices"
        :key="svc.id"
        class="service-chip"
      >
        <input
          type="checkbox"
          :checked="checkedIds.has(svc.id)"
          @change="toggleCheck(svc.id)"
          style="accent-color: #1f6feb; width: 11px; height: 11px; cursor: pointer;"
        />
        <span class="dot" :style="{ background: statusColor(svc.status) }" />
        <span class="svc-name">{{ svc.name }}</span>
      </div>
    </div>

    <template v-if="checkedIds.size > 0">
      <div class="divider" />
      <button class="action-btn" @click="restartChecked">↺ 重启</button>
      <button class="action-btn danger" @click="stopChecked">⏹ 停止</button>
    </template>

    <div class="divider" />

    <!-- 同步录制 -->
    <label class="sync-label">
      <input type="checkbox" :checked="syncEnabled" @change="toggleSync" style="accent-color:#1f6feb;" />
      <span>同步录制</span>
    </label>
    <button
      v-if="syncEnabled"
      class="sync-record-btn"
      :class="{ recording: syncRecording }"
      @click="toggleSyncRecord"
    >
      {{ syncRecording ? '⏹' : '⏺' }}
    </button>

    <div class="flex-1" />

    <!-- Agent 状态 -->
    <div class="agent-status">
      <span class="agent-dot" :class="{ connected: agentStore.connected }" />
      <span>127.0.0.1:27017</span>
    </div>
  </div>
</template>

<style scoped>
.bottom-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  background: var(--bg-elevated);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  min-height: 30px;
  overflow-x: auto;
}
.label {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
  flex-shrink: 0;
}
.service-chips { display: flex; gap: 10px; align-items: center; }
.service-chip { display: flex; align-items: center; gap: 4px; }
.dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.svc-name { font-size: 11px; color: var(--text-primary); white-space: nowrap; }

.divider { width: 1px; height: 14px; background: var(--border); flex-shrink: 0; }
.flex-1 { flex: 1; }

.action-btn {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 10px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.action-btn:hover { background: var(--bg-overlay); }
.action-btn.danger {
  border-color: rgba(248,81,73,0.3);
  color: #f85149;
}

.sync-label {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
}
.sync-record-btn {
  background: transparent;
  border: none;
  font-size: 15px;
  cursor: pointer;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
  color: #3fb950;
}
.sync-record-btn.recording { color: #f85149; }

.agent-status {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 10px;
  color: var(--text-tertiary);
  white-space: nowrap;
  flex-shrink: 0;
}
.agent-dot {
  width: 6px; height: 6px;
  border-radius: 50%;
  background: #6e7681;
}
.agent-dot.connected { background: #3fb950; }
</style>
