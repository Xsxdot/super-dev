---
status: accepted
---

# Uninstall remote agents before detaching

Desktop 将 Agent Uninstall 作为正常移除路径：Controller 通过 Host 的 SSH 连接卸载远端 Agent，成功后才删除 Agent 管理配置；远端失败时保留配置，远端已卸载但配置删除失败时允许幂等重试。卸载默认保留 Remote Agent Data，只有用户显式选择并加强确认 Agent Data Purge 时才删除；自动卸载与手动脚本都只能清理 SuperDev Agent 自有的服务、启动项、二进制、数据和日志，不停止或删除 Independent Host Runtime，尽管 Agent Child Process 可能随 Agent 停止。

SSH 自动卸载失败时，Desktop 提供随版本发布、默认保留数据且可重复执行的 `uninstall-agent.sh` 与 `uninstall-agent.ps1`。用户仍无法完成远端卸载时，才显示 Agent Detach 兜底并警告远端 Agent 可能继续运行。旧 Agent 配置删除接口返回 `decommission_required` 而不修改状态；仍被 Agent 配置引用的 Host 返回 `agent_configured`，不得通过级联删除绕过卸载或 Detach。

同一 Host 上冲突的 Agent 生命周期变更使用轻量内存级互斥并返回 `operation_in_progress`，不同 Host 不互相阻塞；不为该短事务引入 preview/revision 协议、持久化工作流或队列。Desktop 用户确认不进入 Operation Approval，也不写持久化 audit，这是基于 Desktop 启动的 Controller 仅监听 loopback、以本机用户为信任边界的明确例外；卸载、Purge 与 Detach 不向 MCP 暴露工具。例外不削弱可观测性要求：开始、结果、失败阶段、Purge 选择和 Detach 原因仍必须写入不含凭据的结构化日志。
