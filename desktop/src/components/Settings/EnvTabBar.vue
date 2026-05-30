<!--
EnvTabBar：环境横向 tab，切换 / 新增 / 删除 / 改名 / is_dev 标记。

职责：
  - 渲染所有 env tab，高亮 active
  - 切换 emit update:active；新增 emit add-env；删除 emit remove-env
  - 改名：父层通过 renamingEnv prop 控制哪个 tab 处于编辑态
  - 点击 ✎ 或双击 tab 触发 start-rename，由父层更新 renamingEnv
边界：
  - 不持有草稿，受控组件
  - 改名校验（重名检测）由父层负责
-->
<script setup lang="ts">
import type { Environment } from '@/api/agent'

defineProps<{ environments: Environment[]; active: string; renamingEnv?: string }>()
const emit = defineEmits<{
  'update:active': [string]
  'add-env': []
  'remove-env': [string]
  'rename-env': [string, string]
  'toggle-dev': [string]
  'start-rename': [string]
}>()

// submitRename 从 input 事件中读取新名称并 emit rename-env；
// 无论是否改名，都 emit start-rename('') 退出编辑态。
function submitRename(oldName: string, event: Event) {
  const newName = (event.target as HTMLInputElement).value.trim()
  if (newName && newName !== oldName) {
    emit('rename-env', oldName, newName)
  }
  emit('start-rename', '')
}
</script>

<template>
  <div class="env-tabbar">
    <button
      v-for="env in environments" :key="env.id || env.name"
      type="button" class="env-tab" data-test="env-tab"
      :class="{ active: env.name === active }"
      @click="env.name !== renamingEnv && emit('update:active', env.name)"
      @dblclick="emit('start-rename', env.name)"
    >
      <template v-if="env.name === renamingEnv">
        <input
          class="env-rename-input"
          data-test="env-rename-input"
          :value="env.name"
          @keydown.enter.prevent="submitRename(env.name, $event)"
          @keydown.esc.prevent="emit('start-rename', '')"
          @blur="submitRename(env.name, $event)"
          @click.stop
          autofocus
        />
      </template>
      <template v-else>
        {{ env.name }}
        <span v-if="env.is_dev" class="dev-dot" title="开发环境">·dev</span>
        <span class="env-edit" data-test="env-edit" title="重命名" @click.stop="emit('start-rename', env.name)">✎</span>
        <span class="env-x" @click.stop="emit('remove-env', env.name)">✕</span>
      </template>
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
  display: flex;
  align-items: center;
  gap: 4px;
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
  color: var(--text-tertiary);
}
.env-x:hover {
  color: var(--status-failed);
}
.env-edit {
  color: var(--text-tertiary);
  font-size: 10px;
  opacity: 0;
}
.env-tab:hover .env-edit,
.env-tab.active .env-edit {
  opacity: 1;
}
.add-env {
  padding: 6px 10px;
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  cursor: pointer;
  font-size: 12px;
}
.env-rename-input {
  width: 80px;
  padding: 0 4px;
  font-size: 12px;
  background: var(--bg-primary);
  border: 1px solid var(--accent);
  color: var(--text-primary);
  outline: none;
}
</style>
