<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useNodeStore } from '@/stores/node'
import { usePortMirrorStore } from '@/stores/portMirror'
import type { Project } from '@/api/agent'
import PopoverProjectList from '@/components/Popover/PopoverProjectList.vue'
import PopoverServicePanel from '@/components/Popover/PopoverServicePanel.vue'

const agentStore = useAgentStore()
const nodeStore = useNodeStore()
const portMirrorStore = usePortMirrorStore()
const hoveredProject = ref<Project | null>(null)

onMounted(() => {
  agentStore.startPolling()
  void nodeStore.start()
  // 目前 Popover* 组件树没有直接消费 portMirrorStore 的 UI（不同于 MainPage 下的
  // EnvGroup/BottomBar）。这里仍然与 nodeStore 同起同停：popover 是独立窗口、有自己的
  // 挂载/卸载生命周期，和 nodeStore 一样按"这个窗口存在期间保持订阅热着"的既有约定处理，
  // 避免这两个同类订阅型 store 在同一窗口里的生命周期不一致。
  void portMirrorStore.start()
})
onUnmounted(() => {
  agentStore.stopPolling()
  nodeStore.stop()
  portMirrorStore.stop()
})

function onProjectHover(project: Project | null) {
  if (project !== null) {
    hoveredProject.value = project
  }
  // null 由 popover-root @mouseleave 统一清除，防止移向右栏时闪烁
}
</script>

<template>
  <div
    class="popover-root"
    @mouseleave="hoveredProject = null"
  >
    <PopoverProjectList @hover="onProjectHover" />
    <div v-if="hoveredProject" class="panel-divider" />
    <PopoverServicePanel
      v-if="hoveredProject"
      :project="hoveredProject"
    />
  </div>
</template>

<style scoped>
.popover-root {
  display: flex;
  height: 100vh;
  background: var(--bg-primary);
  overflow: hidden;
}
.panel-divider {
  width: 1px;
  background: var(--border);
  flex-shrink: 0;
}
</style>
