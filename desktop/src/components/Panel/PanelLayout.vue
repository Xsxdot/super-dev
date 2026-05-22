<script setup lang="ts">
import { computed } from 'vue'
import { usePanelStore, type PanelNode } from '@/stores/panel'
import PanelLeaf from './PanelLeaf.vue'

const props = defineProps<{
  node?: PanelNode
  isRoot?: boolean
}>()

const panelStore = usePanelStore()
const node = computed(() => props.node ?? panelStore.root)

const rootLeafCount = computed(() => panelStore.allLeaves.length)
</script>

<template>
  <div
    v-if="node.type === 'leaf'"
    class="panel-leaf-wrapper"
  >
    <PanelLeaf
      :panel-id="node.id"
      :service-id="node.serviceId"
      :project-id="node.projectId"
      :source="node.source"
      :can-close="rootLeafCount > 1"
    />
  </div>

  <div
    v-else
    class="panel-split"
    :class="node.axis === 'h' ? 'split-h' : 'split-v'"
  >
    <div class="split-first">
      <PanelLayout :node="node.first" :is-root="false" />
    </div>
    <div class="split-divider" :class="node.axis === 'h' ? 'divider-v' : 'divider-h'" />
    <div class="split-second">
      <PanelLayout :node="node.second" :is-root="false" />
    </div>
  </div>
</template>

<style scoped>
.panel-leaf-wrapper {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.panel-split {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.split-h { flex-direction: row; }
.split-v { flex-direction: column; }
.split-first, .split-second {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.split-divider {
  background: var(--border-secondary);
  flex-shrink: 0;
}
.divider-v { width: 1px; cursor: col-resize; }
.divider-h { height: 1px; cursor: row-resize; }
</style>
