<!--
搜索标签页组件

职责：
  - 作为搜索标签页内容入口
  - 在搜索功能接入前显示项目级搜索页面骨架

边界：
  - 不执行搜索请求
  - 不渲染搜索结果看板
-->
<script setup lang="ts">
defineProps<{ tabId: string }>()
</script>

<template>
  <div class="search-page">
    <div class="search-empty">输入关键词搜索当前项目历史日志</div>
  </div>
</template>

<style scoped>
.search-page {
  display: flex;
  flex: 1;
  min-height: 0;
  background: var(--bg-primary);
}
.search-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
