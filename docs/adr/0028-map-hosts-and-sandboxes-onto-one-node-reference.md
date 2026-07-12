---
status: accepted
---

# Map hosts and sandboxes onto one node reference

Controller 引入薄的 Node Reference，使用稳定 Node ID 与 Node Kind 统一寻址 self、remote 和 sandbox 节点。Remote Node 的 Node ID 首版继续等于 Host ID，并携带 Host ID；Sandbox Node 使用 `sandbox:<workspace-id>`，携带 Workspace ID，不随容器、动态端口或 Sandbox Agent 进程变化。Sandbox Node 由 Workspace Registry 与 Observed Sandbox State 动态生成，不写入面向 SSH 机器的 remote Host store。

## Consequences

Sandbox Node 继续构造现有 Agent target 并使用 DirectTransport、Dispatcher、NodeRegistry、health probe、managed projection、日志与调试代理，不新增 SandboxTransport 或第二套节点注册表。动态地址和 token 保存在 Controller 私有 Sandbox 状态。公共节点契约以 `node_id` 为规范方向，现有远程接口的 `host_id` 兼容保留并渐进迁移。UI 在 Workspace 下展示 Sandbox Node，Remote Host CRUD、SSH 安装和远程主机列表不包含这些临时节点。
