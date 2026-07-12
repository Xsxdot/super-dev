---
status: accepted
---

# Reconcile stale sandboxes explicitly

当有效 Sandbox Definition 与当前 Sandbox Revision 不同时，Sandbox 进入 stale 状态，不在后台自动重建。现有 Runtime Instance 可以继续运行，观察和停止操作保持可用，但新的启动、重启、调试和任务执行被阻止，直到 User 或 Coding Agent 显式发起 Sandbox Reconcile。这里的“显式”表示一次带目标 Revision、可预览、可授权和可审计的请求，不要求必须由 User 在界面点击。

## Consequences

首版对任何有效 Definition 变化都采用重新创建，不尝试热更新分类。Sandbox Reconcile 必须把受影响的 Runtime Instance、lifecycle command 和目标 Revision 纳入操作计划及 fingerprint；文件监听器只能更新 stale 状态，不能自行执行 reconcile。
