---
status: accepted
---

# Assign workspace identity in the controller registry

Controller Agent 在 Workspace 首次注册时分配本机稳定 UUID，并将其与 Project ID、当前 RootPath 和可选版本控制身份保存在 Workspace Registry。绝对路径、分支、HEAD、commit、Sandbox 和容器都不直接构成 Workspace ID。Git common dir 与 worktree gitdir 仅用于 worktree 移动后的 rebind；非 Git Workspace 移动需要显式重新关联。

## Consequences

当前只保存路径数组的项目 Registry 需要迁移为 Workspace Record。相同 Project ID 可以关联多个 Workspace，复制配置不再触发 Project 或 Deployment ID 重写。原路径被不同 Git worktree 重新占用时必须分配新 Workspace ID；跨 Controller 主机的全局 Workspace 身份不在首版范围内。
