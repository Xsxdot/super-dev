---
status: accepted
---

# Trust exact sandbox revisions

Sandbox Trust 绑定精确 Sandbox Revision 及其解析后的敏感影响，而不绑定 User 或 Coding Agent。任何请求者都可以检查、预览、申请授权并执行 Sandbox 操作；首次执行或 Definition 变化默认需要新的信任，相同 Revision 和敏感影响后续复用不重复审批。用户可以通过 Approval Policy 允许 Coding Agent 自动执行，但操作仍需留下 requester 和审计记录。

## Consequences

信任 fingerprint 必须包含 Workspace、Sandbox Revision、lifecycle command、主机挂载和安全能力；会停止 Runtime Instance 的 reconcile 还必须包含受影响实例。经批准的 `config.devcontainer.upsert` 可以同时为其精确输出 Revision 建立信任，避免对相同内容二次审批。删除或重置 Sandbox 存储不能复用普通 reconcile 信任。
