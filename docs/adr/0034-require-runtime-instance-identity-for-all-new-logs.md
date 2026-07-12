---
status: accepted
---

# Require runtime instance identity for all new logs

`log_entries.runtime_instance_id` 与 `workspace_id` 在物理 SQLite schema 中保持可空，以无损兼容旧数据库；但升级后 Host、Remote 与 Sandbox 的所有新 Runtime Log 写入都必须提供两者。NULL 只表示 Legacy Unscoped Runtime Log，不能表示 Host placement。Runtime Instance Identity 继续由 Workspace、Deployment 与 Slot 决定，不随执行位置改变。

## Consequences

迁移只增加 nullable columns，不对大型历史日志表执行全量 UPDATE。旧 `(deployment_id, seq)` 唯一索引改为仅约束非空新身份的 `(runtime_instance_id, seq)` 部分唯一索引；seq watermark、实时订阅和折叠维度统一迁移到 Runtime Instance。查询参数可以兼容性可选：显式 Runtime Instance 精确读取；Deployment 加 Workspace 解析为实例；只有 Deployment 时仅在 Workspace 唯一时解析，否则返回 Ambiguous Workspace。Legacy 行保留为独立 lane，Workspace-scoped 查询默认不混入，旧 deployment 查询或全局搜索可显式包含并清楚标记来源未分配。
