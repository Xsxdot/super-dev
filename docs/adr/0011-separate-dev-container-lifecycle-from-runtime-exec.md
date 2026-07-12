---
status: superseded by ADR-0012
---

# Separate dev container lifecycle from runtime exec

Host SuperDev Agent 使用 Dev Container CLI 解析和管理 Sandbox 的环境级生命周期，并在 Prepare 后取得 container ID、容器内 Workspace 路径、远端用户和有效环境；具体 Runtime Instance 的启动、attach、状态采样和日志流则通过 Container Engine adapter 管理。Dev Container CLI 不承担长期服务进程的稳定运行句柄职责，Container Engine 也不重新解析或实现 Dev Container 配置语义。

## Consequences

首个 Container Engine adapter 使用 Docker-compatible Engine，未来可以替换为 Podman 等实现。SandboxHandle 必须保存从 Dev Container Driver 解析出的用户、路径、环境和 Revision，保证 Engine Exec 与 Dev Container 用户环境一致；未来加入 Sandbox Worker 时，只替换执行 adapter，不改变上层 Runtime Instance 生命周期。
