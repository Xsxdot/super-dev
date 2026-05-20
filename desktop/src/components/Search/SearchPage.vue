<!--
搜索标签页组件

职责：
  - 提供项目级历史日志搜索入口
  - 渲染搜索状态：空、加载、结果、无结果、失败

边界：
  - 不直接访问 agent API，通过 workspaceStore 执行搜索
  - 不实现右侧分栏细节，交给 SearchBoard
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import SearchBoard from './SearchBoard.vue'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const input = ref('')

const tab = computed(() => workspace.searchTab(props.tabId))
const project = computed(() => tab.value ? agentStore.projectById(tab.value.projectId) : null)

watch(tab, value => {
  input.value = value?.query ?? ''
}, { immediate: true })

function submit() {
  if (!tab.value) return
  void workspace.runSearch(tab.value.id, input.value)
}
</script>

<template>
  <div v-if="tab" class="search-page">
    <div class="search-top">
      <div class="project-name">{{ project?.name ?? tab.projectId }}</div>
      <form class="search-form" @submit.prevent="submit">
        <input
          v-model="input"
          class="search-input"
          placeholder="输入 traceID、orderID、错误关键字..."
          autofocus
        >
        <button class="search-button" :disabled="tab.status === 'loading'">搜索</button>
      </form>
      <div v-if="tab.status === 'results'" class="result-summary">
        {{ tab.results.length }} / {{ Object.values(tab.serviceCounts).reduce((a, b) => a + b, 0) }} 条命中
      </div>
    </div>

    <div v-if="tab.status === 'empty'" class="search-empty">
      <div class="search-brand">Trace Search</div>
    </div>
    <div v-else-if="tab.status === 'loading'" class="search-state">搜索中...</div>
    <div v-else-if="tab.status === 'emptyResults'" class="search-state">当前项目没有匹配日志</div>
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
</style>
