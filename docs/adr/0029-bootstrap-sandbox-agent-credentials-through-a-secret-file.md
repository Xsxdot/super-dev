---
status: accepted
---

# Bootstrap sandbox agent credentials through a secret file

Sandbox Agent 复用现有 pending-bootstrap 与 `/api/security/provision` 协议。Controller 为新 Sandbox Node 生成一次性 Sandbox Bootstrap Secret 与长期 Node Credential，通过 Container Engine 的临时 `0600` secret file 供给新增的 `--bootstrap-token-file` 参数；secret 不得出现在命令行、环境变量、label、Sandbox Definition 或日志。Controller 经 Host loopback Endpoint 完成幂等 provision 后立即删除两端的一次性 secret，Sandbox Agent State 只持久化长期 token hash。

## Consequences

Node Credential 明文只保存在 Controller DataDir 中按 Node ID 索引的私有 credential store；现有 Host 维度 AgentSecret 应抽取到 Node 维度语义。Controller 在 provision 完成前保留可恢复的 bootstrap secret，因此崩溃重试不会产生不可解锁的 pending state。后续 Agent 重启复用长期凭据。若 Controller credential 丢失但 Sandbox Agent State 仍存在，禁止自动关闭认证或删除全部 Agent State，必须执行显式高风险 Sandbox Credential Repair，仅轮换安全状态。首版本机控制通道继续使用 loopback HTTP 加 bearer token，不启用 TLS。
