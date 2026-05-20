<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useLogStore } from '@/stores/log'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'
import { useAgentStore } from '@/stores/agent'
import PanelToolbar from './PanelToolbar.vue'
import LogRow from './LogRow.vue'
import type { LogEntry } from '@/api/agent'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
}>()

const logStore = useLogStore()
const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()
const agentStore = useAgentStore()

const viewingRunId = ref<string | null>(null)
const isFollowing = ref(true)
const newLogCount = ref(0)
const logListEl = ref<HTMLElement | null>(null)

// 订阅 WebSocket
onMounted(() => {
  if (props.serviceId) logStore.subscribe(props.serviceId)
  if (props.projectId) filterStore.loadProjectRules(props.projectId)
})
onUnmounted(() => {
  if (props.serviceId) logStore.unsubscribe(props.serviceId)
  filterStore.removePanel(props.panelId)
})
watch(() => props.serviceId, (newId, oldId) => {
  if (oldId) logStore.unsubscribe(oldId)
  if (newId) logStore.subscribe(newId)
  viewingRunId.value = null
  isFollowing.value = true
})

// 原始日志（实时或历史）
const rawLogs = computed<LogEntry[]>(() => {
  if (viewingRunId.value && props.serviceId) {
    return logStore.getHistoryLogs(props.serviceId)
  }
  if (props.serviceId) return logStore.getLogs(props.serviceId)
  // 项目视图：合并所有服务日志
  if (props.projectId) {
    const proj = agentStore.projectById(props.projectId)
    if (!proj) return []
    return proj.services
      .flatMap(s => logStore.getLogs(s.id))
      .sort((a, b) => a.id - b.id)
  }
  return []
})

// 过滤后的日志
const filteredLogs = computed(() =>
  filterStore.applyFilters(props.panelId, props.projectId, rawLogs.value)
)

// 书签高亮判断
const bookmark = computed(() => bookmarkStore.getBookmark(props.panelId))
function isHighlighted(log: LogEntry): boolean {
  const bm = bookmark.value
  if (!bm || !bm.startTime) return false
  const ts = new Date(log.timestamp)
  if (bm.state === 'recording') return ts >= bm.startTime
  if (bm.state === 'done' && bm.endTime) return ts >= bm.startTime && ts <= bm.endTime
  return false
}

// 书签录制：追加过滤后的新日志
watch(filteredLogs, (newLogs, oldLogs) => {
  const bm = bookmark.value
  if (!bm || bm.state !== 'recording') return
  const added = newLogs.slice(oldLogs.length)
  for (const log of added) {
    bookmarkStore.appendToBookmark(props.panelId, log)
  }
})

// 自动跟随底部
async function scrollToBottom() {
  await nextTick()
  if (logListEl.value) {
    logListEl.value.scrollTop = logListEl.value.scrollHeight
  }
}

watch(filteredLogs, (newLogs, oldLogs) => {
  const added = newLogs.length - oldLogs.length
  if (added <= 0) return
  if (isFollowing.value) {
    scrollToBottom()
  } else {
    newLogCount.value += added
  }
})

function onScroll() {
  if (!logListEl.value) return
  const { scrollTop, scrollHeight, clientHeight } = logListEl.value
  const distFromBottom = scrollHeight - scrollTop - clientHeight
  if (distFromBottom < 50) {
    isFollowing.value = true
    newLogCount.value = 0
  } else {
    isFollowing.value = false
  }
}

function jumpToBottom() {
  isFollowing.value = true
  newLogCount.value = 0
  scrollToBottom()
}

// 历史记录
const historyRunIds = computed(() =>
  props.serviceId ? logStore.getRunIds(props.serviceId) : []
)

async function selectRun(runId: string | null) {
  viewingRunId.value = runId
  if (runId && props.serviceId) {
    await logStore.loadHistoryLogs(props.serviceId, runId)
  }
  isFollowing.value = false
  await nextTick()
  scrollToBottom()
}

// 统计
const stats = computed(() => {
  let errors = 0, warns = 0
  for (const log of filteredLogs.value) {
    if (log.level === 'ERROR') errors++
    else if (log.level === 'WARN') warns++
  }
  return { total: filteredLogs.value.length, errors, warns }
})

onMounted(scrollToBottom)
</script>

<template>
  <div class="log-panel">
    <PanelToolbar
      :panel-id="panelId"
      :service-id="serviceId"
      :project-id="projectId"
      :history-run-ids="historyRunIds"
      :viewing-run-id="viewingRunId"
      @select-run="selectRun"
    />

    <!-- 历史 banner -->
    <div v-if="viewingRunId" class="history-banner">
      <span>🕐 查看历史记录 · {{ stats.total }} 条</span>
      <button @click="selectRun(null)">返回实时</button>
    </div>

    <!-- 日志列表 -->
    <div
      ref="logListEl"
      class="log-list"
      @scroll="onScroll"
    >
      <LogRow
        v-for="log in filteredLogs"
        :key="log.id"
        :log="log"
        :highlighted="isHighlighted(log)"
      />
    </div>

    <!-- 新日志提示 -->
    <Transition name="fade">
      <button v-if="!isFollowing && newLogCount > 0" class="new-log-pill" @click="jumpToBottom">
        ↓ {{ newLogCount }} 条新日志
      </button>
    </Transition>

    <!-- 状态栏 -->
    <div class="status-bar">
      <span>{{ viewingRunId ? '历史' : '实时' }} · 显示 {{ stats.total }} 条</span>
      <div class="status-badges">
        <span v-if="stats.errors > 0" class="badge error">● {{ stats.errors }} 错误</span>
        <span v-if="stats.warns > 0" class="badge warn">● {{ stats.warns }} 警告</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  position: relative;
}
.history-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 12px;
  background: rgba(210,153,34,0.15);
  border-bottom: 1px solid var(--border-secondary);
  font-size: 12px;
  flex-shrink: 0;
}
.history-banner button {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--text-secondary);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
}
.log-list {
  flex: 1;
  overflow-y: auto;
  background: var(--bg-primary);
  padding: 4px 0;
}
.new-log-pill {
  position: absolute;
  bottom: 32px;
  right: 12px;
  background: #1f6feb;
  color: #fff;
  border: none;
  border-radius: 12px;
  padding: 4px 12px;
  font-size: 11px;
  font-weight: 500;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0,0,0,0.4);
}
.status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 10px;
  background: var(--bg-elevated);
  border-top: 1px solid var(--border-secondary);
  font-size: 10px;
  color: var(--text-tertiary);
  flex-shrink: 0;
}
.status-badges { display: flex; gap: 8px; }
.badge { font-size: 9px; padding: 1px 6px; border-radius: 3px; }
.badge.error { color: #f85149; background: rgba(248,81,73,0.1); }
.badge.warn { color: #d29922; background: rgba(210,153,34,0.1); }

.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
