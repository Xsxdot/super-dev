---
status: accepted
---

# Scope sandbox lifecycle to workspace

Sandbox 是 Workspace 级长期资源，第一次需要隔离执行时懒创建，并由同一 Workspace 的 Coding Agent、Runtime Instance 和调试会话共同复用。停止 Service、结束调试会话或断开 Coding Agent 都不删除 Sandbox；SuperDev Agent 正常退出时停止受管 Runtime Instance 和 Sandbox，但保留可复用的容器与存储，后续启动时重新识别。

## Considered Options

拒绝按运行或调试会话创建临时 Sandbox，因为镜像准备、工具链安装和依赖恢复的成本会落到每次调试路径上。Sandbox 仅在用户显式删除或后续协调新的 Sandbox Definition 时被替换。

## Consequences

Sandbox 必须拥有独立于 Run ID 和 Debug Session 的稳定身份及状态，并能在 SuperDev Agent 重启后通过持久化记录或运行时标签恢复。删除、停止和停止单个 Runtime Instance 是三个不同操作。
