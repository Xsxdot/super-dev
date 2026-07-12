---
status: accepted
---

# Separate stop, recreate, reset, and cache purge

Sandbox 停止、按新 Revision 重建、恢复干净环境和清理共享缓存是四种不同操作。Stop 只释放运行资源；Recreate 替换容器并保留兼容 Workspace State；Sandbox Reset 删除容器、Workspace State 和 Sandbox Artifact，但保留 Tool Download Cache；Tool Cache Purge 独立删除共享下载缓存。不得提供把这些语义混在一起的通用 remove 操作。

## Consequences

Workspace 注销只停止 Sandbox、移除注册关系并把私有状态标记为 Orphaned Workspace State，不删除源码，也不立即删除私有存储。Sandbox Reset 和 Tool Cache Purge 必须分别预览准确的容器、volume、影响实例及预计释放空间，并使用独立高风险授权，不能复用 Sandbox Trust 或普通 Reconcile 授权。
