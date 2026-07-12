---
status: accepted
---

# Expose a minimal sandbox control surface

Sandbox 对外只新增 `list_workspaces`、`get_sandbox_status`、`get_sandbox_operation` 与 `tail_sandbox_operation_logs` 四个只读能力。现有 config preview/apply 增加受限的 `config.sandbox_definition.upsert`；现有 operation preview 增加 execution-mode、prepare、reconcile、stop、reset 与 credential-repair kinds；新增一个统一 `apply_sandbox_operation` 执行带 preconditions、approval token 和可选 wait timeout 的计划。底层 Container Engine、Dev Container CLI、动态端口与 bootstrap 细节不成为 MCP 契约。

## Consequences

现有 `start_service` 与 `restart_service` 先按 Workspace Execution Mode 路由。Sandbox ready 时直接发送 Runtime Command；absent 或 stopped 且 definition、trust、revision 均满足时触发或加入 Sandbox EnsureReady。超过调用等待时间返回 `operation_in_progress` 与 operation ID，不伪装成失败。缺少配置、信任、reconcile、修复或存在冲突时返回 Sandbox Operation Blocker 与明确 next action，不隐式写配置、批准风险或回退 Host。`stop_service` 不为停止一个离线 Runtime 而启动 Sandbox。User、UI 与 Coding Agent 使用同一 HTTP、operation 和授权契约。
