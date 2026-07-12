---
status: accepted
---

# Store sandbox operation logs on the controller

Sandbox Operation Log 是独立于 Runtime Log 和 Pipeline Run Log 的第三条日志链，由 Controller 按 `sandbox_operation_id` 与单调 seq 持久化到 `logs.db`。它记录 Sandbox resolve、preflight、build、create、agent start、handshake、endpoint publish、ready 和 cleanup 等结构化阶段，以及 Container Engine 或 Dev Container CLI 的 stdout/stderr。因为这些输出产生时 Sandbox Agent 可能尚未启动，所以不得保存到 Sandbox Agent State 或伪装成 Runtime Log。

## Consequences

现有 Pipeline RunHub 的历史回放与实时订阅机制应被抽取复用，但 Sandbox Lifecycle Operation 保持独立 owner type 和 API。Coding Agent 通过 `get_sandbox_operation` 与 `tail_sandbox_operation_logs` 查询，UI 使用同一历史和实时事件源；现有 runtime `tail_logs` 不改变语义。所有输出在持久化前统一脱敏，认证 token、完整环境变量和 `.env` 内容不得进入日志。每次 operation 设置明确的大小上限，超过后写入 truncation marker，避免构建输出无限占用 Controller 数据库。
