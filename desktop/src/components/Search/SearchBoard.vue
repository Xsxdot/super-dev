<!--
跨服务搜索结果看板

职责：
  - 左侧展示命中服务和命中时间线
  - 右侧展示按服务分栏的上下文

边界：
  - 不执行搜索请求
  - 不修改项目级过滤规则
-->
<script setup lang="ts">
import SearchServiceRail from './SearchServiceRail.vue'
import SearchServiceColumns from './SearchServiceColumns.vue'
import SearchTimeline from './SearchTimeline.vue'

defineProps<{ tabId: string }>()
</script>

<template>
  <div class="search-board">
    <aside class="search-left">
      <SearchServiceRail :tab-id="tabId" />
      <SearchTimeline :tab-id="tabId" />
    </aside>
    <section class="search-right">
      <SearchServiceColumns :tab-id="tabId" />
    </section>
  </div>
</template>

<style scoped>
.search-board {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.search-left {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-primary);
}
.search-right {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--bg-primary);
}
</style>
