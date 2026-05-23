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
interface RemoteSearchTarget {
  serviceId: string
  serviceName: string
  hostId: string
  hostName: string
}

interface ScopeOption {
  id: string
  name: string
}

const props = defineProps<{
  tabId?: string
  logSourceId?: string | null
  groupKey?: string | null
}>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const remote = useRemoteStore()
const input = ref('')
const queryError = ref('')
const selectedServiceIds = ref<string[]>([])
const selectedHostIds = ref<string[]>([])
const remoteStatus = ref<SearchStatus>('empty')
const remoteResults = ref<RemoteLogEntry[]>([])
const remoteHostsFailed = ref<string[]>([])
const remoteTotalByHost = ref<Record<string, number>>({})
const remoteCursor = ref<string | null>(null)
const remoteHasMore = ref(false)
const remoteError = ref<string | null>(null)

const legacyRemote = computed(() => !!props.logSourceId && !!props.groupKey)
const tab = computed(() => props.tabId ? workspace.searchTab(props.tabId) : null)
const remoteTab = computed(() => props.tabId ? workspace.remoteSearchTab(props.tabId) : null)
const projectRemote = computed(() => !!remoteTab.value?.projectId)
const isRemote = computed(() => legacyRemote.value || projectRemote.value)
const project = computed(() => tab.value ? agentStore.projectById(tab.value.projectId) : null)
const remoteProject = computed(() => remoteTab.value?.projectId ? agentStore.projectById(remoteTab.value.projectId) : null)
const remoteTotal = computed(() =>
  Object.values(remoteTotalByHost.value).reduce((sum, count) => sum + count, 0),
)
const remoteProjectTotal = computed(() =>
  remoteTab.value?.serviceColumns.reduce((sum, column) => sum + column.result_count, 0) ?? 0,
)

const remoteProjectServiceNames = computed(() => new Map(
  remoteProject.value?.services.map(service => [service.id, service.name]) ?? [],
))

const projectRemoteTargets = computed<RemoteSearchTarget[]>(() => {
  const tabValue = remoteTab.value
  if (!tabValue?.projectId) return []
  const projectServices = remoteProjectServiceNames.value
  return remote.logSources.flatMap(source => {
    const serviceId = source.service_id
    if (source.project_id !== tabValue.projectId || !serviceId || !projectServices.has(serviceId)) return []
    const serviceName = projectServices.get(serviceId) ?? serviceId
    return source.host_ids.flatMap(hostId => {
      const host = remote.hostById(hostId)
      if (!host || (tabValue.groupKey !== 'all' && !host.tags.includes(tabValue.groupKey))) return []
      return [{
        serviceId,
        serviceName,
        hostId: host.id,
        hostName: host.name,
      }]
    })
  })
})

const hasSearchableRemoteTargets = computed(() => !projectRemote.value || projectRemoteTargets.value.length > 0)

function uniqueScopeOptions(targets: RemoteSearchTarget[], key: 'service' | 'host'): ScopeOption[] {
  const byId = new Map<string, string>()
  for (const target of targets) {
    byId.set(
      key === 'service' ? target.serviceId : target.hostId,
      key === 'service' ? target.serviceName : target.hostName,
    )
  }
  return [...byId.entries()].map(([id, name]) => ({ id, name }))
}

const serviceOptions = computed(() => uniqueScopeOptions(projectRemoteTargets.value, 'service'))

const scopedRemoteTargets = computed(() => {
  const selected = new Set(selectedServiceIds.value)
  if (selected.size === 0) return projectRemoteTargets.value
  return projectRemoteTargets.value.filter(target => selected.has(target.serviceId))
})

const hostOptions = computed(() => uniqueScopeOptions(scopedRemoteTargets.value, 'host'))

const selectedRemoteScope = computed(() => ({
  serviceIds: [...selectedServiceIds.value],
  hostIds: [...selectedHostIds.value],
}))

const searchInFlight = computed(() =>
  legacyRemote.value ? remoteStatus.value === 'loading' : remoteTab.value?.status === 'loading' || tab.value?.status === 'loading',
)

const submitDisabled = computed(() =>
  (projectRemote.value && !hasSearchableRemoteTargets.value) || searchInFlight.value,
)

const remoteProjectFailureLabels = computed(() =>
  remoteTab.value?.failures.map(item => item.host_id).join(', ') ?? '',
)

const localSearchTotal = computed(() =>
  tab.value ? Object.values(tab.value.serviceCounts).reduce((a, b) => a + b, 0) : 0,
)

watch([tab, remoteTab], ([localValue, remoteValue]) => {
  if (legacyRemote.value) return
  input.value = remoteValue?.query ?? localValue?.query ?? ''
  selectedServiceIds.value = remoteValue?.selectedServiceIds ?? []
  selectedHostIds.value = remoteValue?.selectedHostIds ?? []
}, { immediate: true })

watch(hostOptions, options => {
  const allowed = new Set(options.map(option => option.id))
  selectedHostIds.value = selectedHostIds.value.filter(hostId => allowed.has(hostId))
})

async function runSearch(append = false) {
  if (legacyRemote.value && props.logSourceId && props.groupKey) {
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

  if (remoteTab.value?.projectId) {
    await workspace.runRemoteSearch(remoteTab.value.id, input.value, selectedRemoteScope.value)
    return
  }

  if (!tab.value) return
  await workspace.runSearch(tab.value.id, input.value)
}


function submit() {
  queryError.value = ''
  if (projectRemote.value && !hasSearchableRemoteTargets.value) return
  if (isRemote.value && !input.value.trim()) {
    queryError.value = '请输入搜索内容'
    return
  }
  void runSearch(false)
}

function loadMoreProjectRemote() {
  if (!remoteTab.value) return
  void workspace.loadMoreRemoteSearch(remoteTab.value.id)
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
        <template v-if="legacyRemote">Remote · {{ groupKey }}</template>
        <template v-else-if="remoteTab">Remote Search · {{ remoteProject?.name ?? remoteTab.projectId }}</template>
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
          data-test="remote-search-submit"
          :disabled="submitDisabled"
        >
          搜索
        </button>
      </form>
      <div v-if="queryError" class="query-error" data-test="remote-search-query-error">{{ queryError }}</div>
      <div v-if="legacyRemote && remoteStatus === 'results'" class="result-summary">
        {{ remoteResults.length }} / {{ remoteTotal }} 条命中
      </div>
      <div v-else-if="remoteTab?.status === 'results' || remoteTab?.status === 'partialFailed'" class="result-summary">
        {{ remoteProjectTotal }} 条命中
      </div>
      <div v-else-if="tab?.status === 'results'" class="result-summary">
        {{ tab.results.length }} / {{ localSearchTotal }} 条命中
      </div>
    </div>

    <template v-if="legacyRemote">
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
    <template v-else-if="remoteTab">
      <div v-if="projectRemote" class="scope-bar">
        <div class="scope-group" data-test="remote-search-service-scope">
          <span>{{ selectedServiceIds.length ? '已选服务' : '全部服务' }}</span>
          <label v-for="option in serviceOptions" :key="option.id" class="scope-option">
            <input
              v-model="selectedServiceIds"
              type="checkbox"
              :value="option.id"
              :data-test="`remote-search-service-${option.id}`"
            >
            {{ option.name }}
          </label>
        </div>
        <div class="scope-group" data-test="remote-search-node-scope">
          <span>{{ selectedHostIds.length ? '已选节点' : '全部节点' }}</span>
          <label v-for="option in hostOptions" :key="option.id" class="scope-option">
            <input
              v-model="selectedHostIds"
              type="checkbox"
              :value="option.id"
              :data-test="`remote-search-node-${option.id}`"
            >
            {{ option.name }}
          </label>
        </div>
      </div>
      <div v-if="projectRemote && !hasSearchableRemoteTargets" class="search-state" data-test="remote-search-no-targets">没有可搜索的远程日志对象</div>
      <div v-else-if="remoteTab.status === 'empty'" class="search-empty">
        <div class="search-brand">Remote Search</div>
      </div>
      <div v-else-if="remoteTab.status === 'loading'" class="search-state">搜索中...</div>
      <div v-else-if="remoteTab.status === 'emptyResults'" class="search-state">当前远程项目没有匹配日志</div>
      <div v-else-if="remoteTab.status === 'error' || remoteTab.status === 'failed'" class="search-state error">{{ remoteTab.error ?? '远程搜索失败' }}</div>
      <div v-else class="remote-project-results">
        <div v-if="remoteTab.failures.length > 0" class="hosts-failed">
          部分节点超时或失败：{{ remoteProjectFailureLabels }}
        </div>
        <div v-if="remoteTab.hasMore" class="has-more" data-test="remote-search-has-more">还有更多结果</div>
        <SearchBoard :tab-id="remoteTab.id" />
        <button
          v-if="remoteTab.hasMore"
          class="load-more"
          type="button"
          data-test="remote-search-load-more"
          @click="loadMoreProjectRemote"
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
.query-error {
  color: #f85149;
  font-size: 11px;
}
.scope-bar {
  display: flex;
  gap: 16px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-primary);
  color: var(--text-secondary);
  font-size: 11px;
}
.scope-group,
.scope-option {
  display: flex;
  align-items: center;
  gap: 8px;
}
.scope-group > span {
  color: var(--text-tertiary);
  font-weight: 700;
}
.has-more {
  padding: 6px 12px;
  color: var(--text-tertiary);
  font-size: 11px;
  border-bottom: 1px solid var(--border-secondary);
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
.remote-project-results {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
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
