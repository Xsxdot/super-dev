---
status: accepted
---

# Mount worktree git metadata read-only

Sandbox 在 Compatible Write Policy 下可以修改 Workspace 源码，但 Git Metadata Access 首版固定为只读。Controller 检测主工作树或 linked worktree 的 `.git`、worktree admin dir 和 Git common dir，并将缺失于 Workspace mount 的 Host 路径按原绝对位置只读挂载，使现有 `.git` 指针在容器内可解析；同时设置 `GIT_OPTIONAL_LOCKS=0`，支持构建所需的 Git 读取。

## Consequences

Git commit、checkout、reset、update-index、submodule update 和 LFS checkout 等写操作继续由 Host Coding Agent 执行。额外 Git mount 即使只读也必须进入 Sandbox Trust fingerprint。不得为了兼容 lifecycle command 静默改为可写，因为 Git common dir 被多个 worktree 共享，可写挂载会突破 Workspace 隔离并产生 ref、hook 和 lock 竞争。
