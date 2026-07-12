---
status: accepted
---

# Pin one controller-wide sandbox provider profile

Controller 首次启用 Sandbox 时探测并选择一个本机 Sandbox Provider Profile，随后持久化 driver、endpoint/context、Engine fingerprint 与 capabilities。首版整个 Controller 只有一个 active profile，所有 operation 都显式使用它，不跟随终端 `docker context`、`DOCKER_HOST` 或 Coding Agent 环境变量。

## Consequences

每次 lifecycle operation 校验 Engine fingerprint；endpoint 背后的 Engine 被替换或重置时返回 Engine Change，不能把 owned resources 不可见误判为普通 absent 并直接重建。切换 Docker-compatible Engine 必须 preview/apply，列出受影响 Sandbox 与 Runtime Instance；旧 Engine 可达时先明确停止，旧 Engine 不可达时默认阻塞，强制切换需要高风险授权并警告旧进程可能仍在运行。资源不自动跨 Engine 迁移或删除，旧 profile 元数据保留以便重新连接和清理。Workspace、Sandbox Revision 与项目配置不记录具体品牌或 socket。
