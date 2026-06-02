<!--
RuntimeStatusTab：项目概览页运行状态视图占位。

职责：
  - 为项目概览页提供可解析的运行状态 tab 组件入口

边界：
  - 当前不拉取 runtime-status 数据
  - 真实轮询与实例卡片渲染在后续任务补齐
-->
<script setup lang="ts">
defineProps<{ projectId: string; active: boolean }>()
</script>

<template>
  <section class="runtime-status-tab" />
</template>
