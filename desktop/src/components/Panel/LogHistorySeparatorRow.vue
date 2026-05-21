<!--
历史日志分隔行

职责：
  - 标出日志面板中最近历史日志与实时输出的边界

边界：
  - 不代表真实 LogEntry
  - 不参与复制、导出或过滤
-->
<template>
  <div class="history-separator-row">
    <span class="line" />
    <span class="label">历史消息 · 之后为实时输出</span>
    <span class="line" />
  </div>
</template>

<style scoped>
.history-separator-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  color: var(--text-tertiary);
  font-size: 10px;
}
.line {
  height: 1px;
  flex: 1;
  background: var(--border-secondary);
}
.label {
  white-space: nowrap;
}
</style>
