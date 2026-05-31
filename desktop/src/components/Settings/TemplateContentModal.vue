<!--
TemplateContentModal：模板内容只读查看弹窗。

职责：
  - 展示模板名称与 YAML 内容
  - 展示加载和错误状态
  - 通过 close 事件交还关闭控制权

边界：
  - 不加载模板详情数据
  - 不编辑或保存模板内容
-->
<script setup lang="ts">
defineProps<{ open: boolean; title: string; yaml: string; loading?: boolean; error?: string }>()
defineEmits<{ close: [] }>()
</script>

<template>
  <div v-if="open" class="modal-backdrop">
    <section class="modal-panel" role="dialog" aria-modal="true">
      <header class="modal-head">
        <h2>{{ title }}</h2>
        <button type="button" data-test="template-modal-close" @click="$emit('close')">关闭</button>
      </header>
      <div v-if="loading" class="modal-state">加载中...</div>
      <div v-else-if="error" class="modal-error">{{ error }}</div>
      <pre v-else class="yaml-view"><code>{{ yaml }}</code></pre>
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
.modal-panel {
  width: min(860px, calc(100vw - 48px));
  max-height: min(720px, calc(100vh - 48px));
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-primary);
  color: var(--text-primary);
  overflow: hidden;
}
.modal-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-secondary);
}
.modal-head h2 {
  margin: 0;
  font-size: 14px;
}
.modal-head button {
  border: 1px solid var(--border-secondary);
  border-radius: 5px;
  background: var(--bg-overlay);
  color: var(--text-secondary);
  cursor: pointer;
  padding: 4px 9px;
  font-size: 11px;
}
.yaml-view {
  margin: 0;
  padding: 12px;
  overflow: auto;
  max-height: 620px;
  font-size: 12px;
  line-height: 1.5;
}
.modal-state,
.modal-error {
  padding: 12px;
  font-size: 12px;
  color: var(--text-secondary);
}
.modal-error {
  color: var(--status-failed);
}
</style>
