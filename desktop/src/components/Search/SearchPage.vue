<!--
搜索标签页组件

职责：
  - 提供项目级历史日志搜索入口
  - 支持远程 LogSource 分组的跨节点搜索
  - 渲染搜索状态：空、加载、结果、无结果、失败

边界：
  - 本地搜索通过 workspaceStore 执行
  - 远程搜索直接调用远程搜索 API，并维护页面内状态
  - 不实现右侧分栏细节，交给 SearchBoard
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useRemoteStore } from '@/stores/remote'
import SearchBoard from './SearchBoard.vue'
import { api, type RemoteLogEntry } from '@/api/agent'
import { tagColor } from '@/lib/tagColor'

type SearchStatus = 'empty' | 'loading' | 'results' | 'emptyResults' | 'error'

const props = defineProps<{
  tabId?: string
  logSourceId?: string | null
  groupKey?: string | null
}>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const remote = useRemoteStore()
const input = ref('')
const remoteStatus = ref<SearchStatus>('empty')
const remoteResults = ref<RemoteLogEntry[]>([])
const remoteHostsFailed = ref<string[]>([])
const remoteTotalByHost = ref<Record<string, number>>({})
const remoteCursor = ref<string | null>(null)
const remoteHasMore = ref(false)
const remoteError = ref<string | null>(null)

const isRemote = computed(() => !!props.logSourceId && !!props.groupKey)
const tab = computed(() => props.tabId ? workspace.searchTab(props.tabId) : null)
const project = computed(() => tab.value ? agentStore.projectById(tab.value.projectId) : null)
const remoteTotal = computed(() =>
  Object.values(remoteTotalByHost.value).reduce((sum, count) => sum + count, 0),
)

watch(tab, value => {
  if (isRemote.value) return
  input.value = value?.query ?? ''
}, { immediate: true })

async function runSearch(append = false) {
  if (isRemote.value && props.logSourceId && props.groupKey) {
    const query = input.value.trim()
    if (!query) return
    remoteStatus.value = 'loading'
    remoteError.value = null
    try {
      const result = await api.remoteSearch({
        log_source_id: props.logSourceId,
        group: props.groupKey,
        query,
        limit: 200,
        cursor: append ? remoteCursor.value ?? undefined : undefined,
      })
      remoteResults.value = append
        ? [...remoteResults.value, ...result.entries]
        : result.entries
      remoteTotalByHost.value = result.total_by_host
      remoteHostsFailed.value = result.hosts_failed
      remoteCursor.value = result.next_cursor
      remoteHasMore.value = result.has_more
      remoteStatus.value = remoteResults.value.length ? 'results' : 'emptyResults'
    } catch (err) {
      remoteError.value = err instanceof Error ? err.message : String(err)
      remoteStatus.value = 'error'
    }
    return
  }

  if (!tab.value) return
  await workspace.runSearch(tab.value.id, input.value)
}

function submit() {
  void runSearch(false)
}

function hostNameFor(entry: RemoteLogEntry): string {
  return remote.hostById(entry.host_id)?.name ?? entry.host_id
}

function hostColorFor(entry: RemoteLogEntry): string {
  const tag = remote.hostById(entry.host_id)?.tags[0]
  return tag ? tagColor(tag) : 'var(--text-tertiary)'
}

defineExpose({ runSearch })
</script>

<template>
  <div v-if="isRemote || tab" class="search-page">
    <div class="search-top">
      <div class="project-name">
        <template v-if="isRemote">Remote · {{ groupKey }}</template>
        <template v-else-if="tab">{{ project?.name ?? tab.projectId }}</template>
      </div>
      <form class="search-form" @submit.prevent="submit">
        <input
          v-model="input"
          class="search-input"
          data-test="search-input"
          placeholder="输入 traceID、orderID、错误关键字..."
          autofocus
        >
        <button
          class="search-button"
          :disabled="isRemote ? remoteStatus === 'loading' : tab?.status === 'loading'"
        >
          搜索
        </button>
      </form>
      <div v-if="isRemote && remoteStatus === 'results'" class="result-summary">
        {{ remoteResults.length }} / {{ remoteTotal }} 条命中
      </div>
      <div v-else-if="tab?.status === 'results'" class="result-summary">
        {{ tab.results.length }} / {{ Object.values(tab.serviceCounts).reduce((a, b) => a + b, 0) }} 条命中
      </div>
    </div>

    <template v-if="isRemote">
      <div v-if="remoteStatus === 'empty'" class="search-empty">
        <div class="search-brand">Remote Search</div>
      </div>
      <div v-else-if="remoteStatus === 'loading'" class="search-state">搜索中...</div>
      <div v-else-if="remoteStatus === 'emptyResults'" class="search-state">当前分组没有匹配日志</div>
      <div v-else-if="remoteStatus === 'error'" class="search-state error">{{ remoteError }}</div>
      <div v-else class="remote-results">
        <div v-if="remoteHostsFailed.length > 0" class="hosts-failed">
          以下节点超时或失败：{{ remoteHostsFailed.join(', ') }}
        </div>
        <div
          v-for="entry in remoteResults"
          :key="`${entry.host_id}:${entry.id}`"
          class="remote-row"
        >
          <span class="remote-host" :style="{ color: hostColorFor(entry) }">[{{ hostNameFor(entry) }}]</span>
          <span class="remote-time">{{ new Date(entry.timestamp).toLocaleTimeString('en-US', { hour12: false }) }}</span>
          <span class="remote-level">{{ entry.level }}</span>
          <span class="remote-message">{{ entry.message }}</span>
        </div>
        <button
          v-if="remoteHasMore"
          class="load-more"
          type="button"
          @click="runSearch(true)"
        >
          加载更多
        </button>
      </div>
    </template>
    <template v-else-if="tab">
      <div v-if="tab.status === 'empty'" class="search-empty">
        <div class="search-brand">Trace Search</div>
      </div>
      <div v-else-if="tab.status === 'loading'" class="search-state">搜索中...</div>
      <div v-else-if="tab.status === 'emptyResults'" class="search-state">当前项目没有匹配日志</div>
      <div v-else-if="tab.status === 'error'" class="search-state error">{{ tab.error }}</div>
      <SearchBoard v-else :tab-id="tab.id" />
    </template>
  </div>
</template>

<style scoped>
.search-page {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  background: var(--bg-primary);
}
.search-top {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  flex-shrink: 0;
}
.project-name {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.search-form {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
}
.search-input {
  flex: 1;
  min-width: 180px;
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 6px 9px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 12px;
  outline: none;
}
.search-button {
  border: 1px solid rgba(88, 166, 255, 0.35);
  border-radius: 5px;
  background: rgba(88, 166, 255, 0.12);
  color: #58a6ff;
  padding: 6px 12px;
  font-size: 12px;
  cursor: pointer;
}
.search-button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.result-summary {
  color: var(--text-tertiary);
  font-size: 11px;
}
.search-empty,
.search-state {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 13px;
}
.search-brand {
  font-size: 22px;
  color: var(--text-secondary);
}
.search-state.error {
  color: #f85149;
}
.remote-results {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px 0;
  font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', monospace;
  font-size: 11px;
}
.hosts-failed {
  margin: 0 12px 8px;
  padding: 6px 8px;
  color: var(--status-failed);
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.3);
}
.remote-row {
  display: flex;
  gap: 6px;
  padding: 1px 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.remote-host,
.remote-time,
.remote-level {
  flex-shrink: 0;
}
.remote-time {
  color: var(--text-tertiary);
}
.remote-level {
  width: 48px;
  color: var(--status-running);
}
.remote-message {
  color: var(--text-primary);
}
.load-more {
  display: block;
  margin: 10px auto;
  padding: 5px 12px;
  color: var(--text-secondary);
  background: var(--bg-overlay);
  border: 1px solid var(--border);
  border-radius: 5px;
  cursor: pointer;
  font-size: 11px;
}
</style>
