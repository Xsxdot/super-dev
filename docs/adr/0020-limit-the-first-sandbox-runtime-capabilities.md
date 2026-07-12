---
status: accepted
---

# Limit the first sandbox runtime capabilities

首版 Sandbox 只接管 `language` 与 `command` Runtime，以 Workspace Execution Mode 整体选择 Host 或 Sandbox。Sandbox Agent 必须报告实际 runtime、language 和 code-debug capabilities；Controller 在 preview 阶段验证。Sandbox 不支持 launchd、systemd、Docker-in-Docker、nginx static、external 或 GUI runtime，且任何不支持项都明确失败，绝不静默回退 Controller 主机。

## Consequences

同一 Workspace 首版不增加逐 Service 的 Host/Sandbox 混合 placement。用户可以显式切换整个 Workspace Execution Mode；Sandbox 模式中的 unsupported deployment 会阻止启动并返回结构化 capability 错误。Sidecar、嵌套 Docker、Docker Socket 和 privileged container 保持后续独立规划，不能作为兼容兜底偷偷启用。
