---
status: accepted
---

# Keep sandbox logs on the sandbox agent

首版只在 Sandbox Agent State 中保存 Runtime Log，Controller Agent 继续通过现有 RemoteAgentBackend 和 NodeTransport 按需查询，不把日志双写或镜像到 Controller 数据库。Service 停止但 Sandbox Node 在线时历史日志可读；Sandbox Node 停止时日志仍保存在 volume 中，但查询明确返回节点离线，读取操作不能隐式启动 Sandbox。

## Consequences

跨节点搜索遇到停止或不可达的 Sandbox Node 时必须返回 Partial Log Result，并列出不可用 Sandbox，不能把缺失来源伪装成完整空结果。Coding Agent 可以显式请求启动 Sandbox 后重试。该选择复用现有远程日志链并避免 seq 同步、断线补偿和双重 retention；若未来需要离线日志搜索，应作为独立复制能力加入。
