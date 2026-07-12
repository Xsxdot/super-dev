---
status: accepted
---

# Limit sandbox garbage collection to owned recoverable state

SuperDev 可以按用户配置的保留时间和容量策略自动执行 Sandbox Garbage Collection，但永不自动删除已注册 Workspace 的私有状态，也不操作缺少完整 SuperDev ownership metadata 的容器或 volume。自动清理范围仅包括超过恢复期的 Orphaned Workspace State、过期 Sandbox Artifact，以及超过容量上限后按最近使用时间淘汰的 Tool Download Cache。

## Consequences

垃圾回收不能调用全局 Docker prune。删除前必须取得 Storage Lease、重新确认没有活动 Sandbox 或 Runtime Instance，并记录资源、原因、释放空间和命中的策略。Workspace 路径暂时不可用不能立即产生 orphaned 判定；只有显式注销或经过重复确认和恢复保留期后才进入候选。策略内自动清理无需逐项审批，但 User 和 Coding Agent 都可以预览或手动触发并查看完整审计。
