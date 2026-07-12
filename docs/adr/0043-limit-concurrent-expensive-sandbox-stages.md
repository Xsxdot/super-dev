---
status: accepted
---

# Limit concurrent expensive sandbox stages

Controller 使用 Sandbox Capacity Gate 限制跨 Workspace 的昂贵 lifecycle stages，首版默认最多同时执行两个 build、create、reconcile 或 reset。每个 Workspace 仍遵守 Workspace Operation Singleflight。超过容量的已接受 operation 显式进入 `waiting_for_capacity`，可以查询、订阅和取消，不等同于把同一 Workspace 的冲突 mutation 隐式排队。

## Consequences

Operation 获得 capacity lease 后必须重新校验 Sandbox Revision、approval 与全部 preconditions，过期则失败并要求重新 preview。Agent start、handshake 和 ready Sandbox 上的 Runtime Command 不占 build slot。并发上限是可配置 Controller policy，但 Coding Agent 不能绕过。首版不为所有容器施加统一 CPU/内存硬配额，因为项目需求差异过大；SuperDev 采集资源指标并报告 Host pressure，后续根据数据再设计 quota policy。
