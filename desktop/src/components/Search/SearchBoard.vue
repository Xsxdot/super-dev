<!--
跨服务搜索结果看板

职责：
  - 承载搜索结果的左侧导航和右侧服务分栏

边界：
  - 当前任务先提供可编译容器，详细分栏在后续任务实现
-->
<script setup lang="ts">
defineProps<{ tabId: string }>()
</script>

<template>
  <div class="search-board">
    <slot />
  </div>
</template>

<style scoped>
.search-board {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
