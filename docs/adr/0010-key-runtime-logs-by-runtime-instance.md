---
status: accepted
---

# Key runtime logs by runtime instance

Runtime Log 归属于 Runtime Instance，而不是单独归属于 Deployment。多个 Workspace 可以同时运行同一个 Deployment，因此日志持久身份和单调序号必须使用 `(runtime_instance_id, seq)`；Workspace ID、Deployment ID 和 Run ID 作为查询与展示维度保留。仅按 Deployment 查询时，如果匹配多个 Runtime Instance，必须返回候选而不能合并日志。

## Consequences

日志模型、SQLite 唯一索引、水位恢复、折叠 lane、实时订阅及 MCP 查询都需要增加 Runtime Instance 维度。首版由 Sandbox Agent 的进程管理器接收 stdout/stderr、分配 seq 并持久化，Controller Agent 经 NodeTransport 和远程日志 backend 向 Coding Agent 提供查询；未来若以 Sandbox Worker 替换完整 Agent，仍沿用同一日志身份契约。
