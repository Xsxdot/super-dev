<!--
EnvTabBar：环境横向 tab，切换 / 新增 / 删除 / is_dev 标记。

职责：
  - 渲染所有 env tab，高亮 active
  - 切换 emit update:active；新增 emit add-env；删除 emit remove-env
边界：
  - 不持有草稿，受控组件
-->
<script setup lang="ts">
import type { Environment } from '@/api/agent'

defineProps<{ environments: Environment[]; active: string }>()
const emit = defineEmits<{
  'update:active': [string]
  'add-env': []
  'remove-env': [string]
}>()
</script>

<template>
  <div class="env-tabbar">
    <button
      v-for="env in environments" :key="env.id || env.name"
      type="button" class="env-tab" data-test="env-tab"
      :class="{ active: env.name === active }"
      @click="emit('update:active', env.name)"
    >
      {{ env.name }}
      <span v-if="env.is_dev" class="dev-dot" title="开发环境">·dev</span>
      <span class="env-x" @click.stop="emit('remove-env', env.name)">✕</span>
    </button>
    <button type="button" class="add-env" data-test="add-env" @click="emit('add-env')">+ 新增环境</button>
  </div>
</template>

<style scoped>
.env-tabbar {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--border-secondary);
  margin-bottom: 12px;
}
.env-tab {
  padding: 6px 12px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 12px;
}
.env-tab.active {
  color: var(--text-primary);
  border-bottom-color: var(--accent);
}
.dev-dot {
  color: var(--accent);
  font-size: 10px;
}
.env-x {
  margin-left: 6px;
  color: var(--text-tertiary);
}
.env-x:hover {
  color: var(--status-failed);
}
.add-env {
  padding: 6px 10px;
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 12px;
}
</style>
