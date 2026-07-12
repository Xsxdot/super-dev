---
status: accepted
---

# Identify runtime instances by workspace, deployment, and slot

Runtime Instance ID 由 Workspace ID、Deployment ID 和 Runtime Slot 稳定生成，首版 Slot 固定为 `default`。停止、重启、Sandbox Recreate、Definition Revision 变化和容器替换都不改变 Runtime Instance ID；每次启动仍使用独立 Run ID。Sandbox、容器、绝对路径、Coding Agent 和 Run ID 不参与实例身份。

## Consequences

进程管理、状态、日志和调试会话使用 Runtime Instance ID 作为实际运行主键，同时保留 Deployment ID 作为定义维度。稳定 ID 生成复用项目现有的摘要算法，但应放在通用身份或 runtime 包中，不能让 runtime 依赖 configchange。Runtime Slot 先保留在模型中而不暴露副本 UI，避免未来支持同 Workspace 多副本时再次迁移身份。
