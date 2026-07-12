---
status: accepted
---

# Model worktrees as project workspaces

同一个 Project 可以同时存在多个 Workspace，每个 Git worktree 是 Project 的一个 Workspace，而不是新的 Project。Sandbox 归属于 Workspace，Deployment 在 Workspace 中运行后形成 Runtime Instance；Coding Agent 只是这些资源的使用者。这样可以保留共享的项目与服务身份，同时让不同代码副本的运行状态、日志和端口彼此隔离，避免把复制配置导致的 ID 重写和生命周期分裂固化为领域模型。

## Considered Options

拒绝把每个 worktree 注册为独立 Project，因为这会割裂共享配置和身份；也拒绝让 Sandbox 归属于 Agent 或单个 Service，因为 Agent 会更替，而同一 Workspace 内的多个 Service 应共享一致的开发环境。
