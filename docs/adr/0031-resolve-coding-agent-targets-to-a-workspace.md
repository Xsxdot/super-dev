---
status: accepted
---

# Resolve coding agent targets to a workspace

所有 Project、Deployment、Runtime、日志和调试操作使用统一 Workspace Target Resolution：显式 `workspace_id` 优先；其次使用 SuperDev MCP 启动时按当前目录映射到最深已注册 Workspace 的 Caller Workspace Context；最后仅在目标 Project 只有一个 Workspace 时选择唯一候选。无法唯一确定时返回 Ambiguous Workspace 与候选列表，绝不默认主 worktree、最近使用 Workspace 或路径排序第一项。

## Consequences

`list_projects` 返回逻辑 Project 及其 `workspaces[]`，并新增 Workspace 查询能力；runtime snapshot 按 Workspace 分组。公共 runtime、日志和调试契约逐步接受 `workspace_id` 与 `runtime_instance_id`。旧 `deployment_id` 仅在唯一可解析时兼容，多 Workspace 下必须显式失败。MCP 在边界解析一次 Workspace context 并传入 Controller，内部链路不重复用路径猜测。Caller context 只提供默认目标，User 与 Coding Agent 仍使用相同授权和 trust 规则。
