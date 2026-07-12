---
status: accepted
---

# Opt in to sandbox execution per workspace

首版所有迁移后的现有 Workspace 与新注册 Workspace 都默认 `execution_mode=host`。存在 `devcontainer.json` 只表示 Sandbox Definition 可解析，不能自动改变 placement。User 或 Coding Agent 必须通过现有 preview/apply 安全门显式执行 Execution Mode Transition；项目级和全局新 Workspace 默认策略延后。

## Consequences

Transition preview 必须列出原 placement 上仍在运行的 Runtime Instance，apply 只有在明确包含停止影响时才能切换，避免 Host 与 Sandbox 同时运行同一实例。切换不自动启动目标环境，也不删除 Sandbox、Workspace State、缓存或日志。Runtime Instance ID 跨 Host/Sandbox 保持稳定，实际进程获得新 Run ID，Observed Node Reference 随 placement 改变。切回 Host 后 Sandbox 保留为 stopped；以后切回 Sandbox 且 revision current 时可以复用。升级 SuperDev 或仅发现 Dev Container Definition 永不隐式迁移现有服务。
