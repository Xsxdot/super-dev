---
status: accepted
---

# Publish declared application ports to dynamic loopback ports

首版 Application Endpoint 在 Sandbox 创建时声明，并由 Container Engine publish 到 Controller 主机的动态 `127.0.0.1` 端口。不同 Workspace 可以使用相同容器内端口而不冲突；Endpoint Binding 是运行态映射，不写回项目配置。应用必须监听容器内 `0.0.0.0`，Agent 生成常见语言 runtime 配置时应加入相应监听参数。

## Consequences

Web 配置需要从持久化绝对 localhost URL 迁移到 protocol、container port 和 path，同时兼容旧 URL。端点集合变化会产生新的 Sandbox Revision 并要求 Reconcile；固定 Host 端口冲突必须失败，默认则由 Engine 动态分配。现有 Dev Container `forwardPorts` 在首版以 publish 能力预检，不能隐瞒 localhost-only 与 `0.0.0.0` 的语义差异。动态 Agent TCP forwarding 和运行中新增端口不在首版范围内。
