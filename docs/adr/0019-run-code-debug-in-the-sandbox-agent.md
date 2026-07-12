---
status: accepted
---

# Run code debug in the sandbox agent

Sandbox Runtime Instance 的 Code Debug Session、DAP adapter 和调试 runtime 全部运行在 Sandbox Agent；Controller Agent 通过 NodeTransport 代理现有代码调试操作，不在 Host 上直接连接容器 PID 或发布 DAP 端口。跨节点源码位置使用 Workspace-relative Source Path，Controller 在 Host Workspace 根与容器 Workspace 根之间执行双向转换和越界校验。

## Consequences

Code Debug Session、runtime map、审计目标和 MCP 参数从 Deployment ID 迁移到 Runtime Instance ID，并保留 Workspace 与 Sandbox Node 元数据。Host 绝对路径在发送前转换为相对路径，Sandbox Agent 继续使用现有项目根内路径校验；DAP 返回的容器路径在 Controller 边界转换回 Host 路径。Browser Debug 仍由 Controller Agent 在 Mac 上运行，并访问动态 Endpoint Binding，不在 Sandbox 内启动浏览器。
