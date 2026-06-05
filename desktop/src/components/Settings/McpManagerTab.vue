<!--
MCP 管理设置页签

职责：
  - 作为设置页 MCP 管理入口的组件边界
  - 后续承载 MCP/skill 状态、更新、卸载和文档展示

边界：
  - 不直接读写 Agent 配置文件，统一通过 api/mcpInstall.ts 调用 Tauri command
  - 不修改 onboarding store
  - 不启动、停止或探测 SuperDev agent 运行态
-->
<template>
  <div />
</template>
