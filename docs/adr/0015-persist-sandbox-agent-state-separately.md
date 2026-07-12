---
status: accepted
---

# Persist sandbox agent state separately

每个 Workspace 拥有独立的 Sandbox Agent State volume，并作为完整 `superdev-agent` 的 DataDir。该状态保存节点身份、安全配置、managed projection、历史日志和审计，跨 Stop、Recreate、Sandbox Reset 及 Agent 二进制升级保留；它不与 Workspace State、Tool Download Cache、Sandbox Artifact 或 Controller Agent DataDir 混用。

## Consequences

Sandbox Reset 不删除 Sandbox Agent State；只有显式重置 Agent State 或最终清理 Workspace 才能删除，并需要独立高风险预览与授权。每次容器替换增加 container generation；generation 变化时保留日志、identity 和 security，但清空旧 PID 与活动进程状态，不在新 PID namespace 中尝试杀死旧 PID。Agent 数据格式升级继续使用现有存储迁移机制。
