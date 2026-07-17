<!--
搜索标签页组件

职责：
  - 提供项目级历史日志搜索入口
  - 渲染搜索状态：空、加载、结果、无结果、失败

边界：
  - 搜索通过 workspaceStore 执行
  - 不实现右侧分栏细节，交给 SearchBoard
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import SearchBoard from './SearchBoard.vue'

const props = defineProps<{
  tabId: string
}>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useI18n()
const input = ref('')

const tab = computed(() => workspace.searchTab(props.tabId))
const project = computed(() => tab.value ? agentStore.projectById(tab.value.projectId) : null)

const localSearchTotal = computed(() =>
  tab.value ? Object.values(tab.value.serviceCounts).reduce((a, b) => a + b, 0) : 0,
)

const matchedServiceCount = computed(() => tab.value ? Object.keys(tab.value.serviceCounts).length : 0)

const visibleResults = computed(() => {
  if (!tab.value) return []
  const hidden = new Set(tab.value.hiddenServiceIds)
  return tab.value.results.filter(entry => !hidden.has(entry.deployment_id))
})

watch(tab, (localValue) => {
  input.value = localValue?.query ?? ''
}, { immediate: true })

async function runSearch() {
  if (!tab.value) return
  await workspace.runSearch(tab.value.id, input.value)
}

function submit() {
  if (!input.value.trim()) return
  void runSearch()
}

function selectedResultIndex(): number {
  if (!tab.value || tab.value.selectedLogId === null) return -1
  return visibleResults.value.findIndex(entry =>
    entry.id === tab.value!.selectedLogId
    && (!tab.value!.selectedLogDeploymentId || entry.deployment_id === tab.value!.selectedLogDeploymentId),
  )
}

function goToRelativeHit(offset: number) {
  if (!tab.value || visibleResults.value.length === 0) return
  const currentIndex = selectedResultIndex()
  const nextIndex =
    currentIndex < 0
      ? offset > 0 ? 0 : visibleResults.value.length - 1
      : Math.min(visibleResults.value.length - 1, Math.max(0, currentIndex + offset))
  const next = visibleResults.value[nextIndex]
  if (!next) return
  void workspace.loadContext(tab.value.id, next)
}
</script>

<template>
  <div v-if="tab" class="search-page">
    <div class="search-top">
      <div class="project-block">
        <div class="project-name">{{ project?.name ?? tab.projectId }}</div>
        <div v-if="tab.status === 'results'" class="project-meta">
          <span data-test="search-service-count">
            {{ t('search.serviceCount', { count: matchedServiceCount }) }}
          </span>
          <span class="meta-separator" />
          <span data-test="search-hit-count">
            {{ t('search.hitCount', { shown: visibleResults.length, total: localSearchTotal }) }}
          </span>
        </div>
      </div>
      <form class="search-form" @submit.prevent="submit">
        <input
          v-model="input"
          class="search-input"
          data-test="search-input"
          :placeholder="t('search.placeholder')"
          autofocus
        >
        <button
          class="search-button"
          data-test="search-submit"
          :disabled="tab.status === 'loading'"
        >
          {{ t('search.submit') }}
        </button>
      </form>
      <div v-if="tab.status === 'results'" class="search-actions">
        <button
          class="nav-hit"
          data-test="search-prev-hit"
          type="button"
          :disabled="visibleResults.length === 0"
          :title="t('search.prevHit')"
          @click="goToRelativeHit(-1)"
        >
          ‹
        </button>
        <button
          class="nav-hit"
          data-test="search-next-hit"
          type="button"
          :disabled="visibleResults.length === 0"
          :title="t('search.nextHit')"
          @click="goToRelativeHit(1)"
        >
          ›
        </button>
      </div>
    </div>
    <div v-if="tab.status === 'empty'" class="search-empty">
      <div class="search-brand">{{ t('search.emptyTitle') }}</div>
    </div>
    <div v-else-if="tab.status === 'loading'" class="search-state">{{ t('search.loading') }}</div>
    <div v-else-if="tab.status === 'emptyResults'" class="search-state">{{ t('search.emptyResults') }}</div>
    <div v-else-if="tab.status === 'error'" class="search-state error">{{ tab.error }}</div>
    <SearchBoard v-else :tab-id="tab.id" />
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
  gap: 12px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  flex-shrink: 0;
}
.project-block {
  display: flex;
  flex-direction: column;
  gap: 3px;
  width: 132px;
  flex: 0 0 132px;
  min-width: 0;
}
.project-name {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.project-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--text-tertiary);
  font-size: 10px;
  line-height: 1;
  white-space: nowrap;
}
.meta-separator {
  width: 3px;
  height: 3px;
  border-radius: 999px;
  background: var(--text-tertiary);
  opacity: 0.7;
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
.search-input:focus {
  border-color: rgba(88, 166, 255, 0.65);
  box-shadow: 0 0 0 2px rgba(88, 166, 255, 0.12);
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
.search-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.nav-hit {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 27px;
  height: 27px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
}
.nav-hit:hover:not(:disabled) {
  border-color: rgba(88, 166, 255, 0.42);
  color: #58a6ff;
  background: rgba(88, 166, 255, 0.08);
}
.nav-hit:disabled {
  opacity: 0.45;
  cursor: not-allowed;
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
</style>
