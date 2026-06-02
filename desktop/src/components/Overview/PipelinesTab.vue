<!--
PipelinesTab：项目概览页流水线视图占位。

职责：
  - 为项目概览页提供可解析的流水线 tab 组件入口

边界：
  - 当前不执行流水线、不拉取运行历史
  - 真实列表、编辑器和执行操作在后续任务补齐
-->
<script setup lang="ts">
import type { Project } from '@/api/agent'

defineProps<{ project: Project }>()
</script>

<template>
  <section class="pipelines-tab" />
</template>
