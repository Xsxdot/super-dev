---
status: accepted
---

# Do not restart stale runtime specs automatically

每个 Workspace-specific managed projection 为 Runtime Instance 携带 desired Runtime Spec Revision；Agent 在启动进程时记录 observed revision 与新 Run ID。命令、working directory、runtime config、effective environment secret versions、readiness 或应用行为变化后可以立即更新 projection，但不得自动重启正在运行的进程。Desired 与 observed 不同形成 Stale Runtime Spec。

## Consequences

Stale Runtime 的 status、日志和 stop 保持可用；对已运行实例调用 start 返回 already-running 与 stale 提示，只有显式 restart 才应用新 spec。Runtime Command 携带 expected runtime spec revision，Agent projection 不匹配时拒绝。删除活动 Deployment 必须在 config preview/apply 中先明确停止，不能先移除 projection 留下无人管理进程。若配置同时改变容器端口、mount 或 capability，它还改变 Sandbox Revision，调用方必须先 Reconcile Sandbox 再 restart Runtime。Secret 明文不进入 revision，只使用稳定 hash 或版本标识；规则对 Host 与 Sandbox 一致。
