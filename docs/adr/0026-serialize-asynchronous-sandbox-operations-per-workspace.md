---
status: accepted
---

# Serialize asynchronous sandbox operations per workspace

Sandbox prepare、reconcile、stop、reset 和修复建模为 Controller 内的异步 Sandbox Lifecycle Operation，并通过稳定 operation identity 暴露阶段、进度和终态。每个 Workspace 使用 Workspace Operation Singleflight：同一目标 Sandbox Revision 的并发 ensure 请求合并到同一 operation，冲突操作返回结构化 `workspace_busy` 与当前 operation，不进入稍后可能意外执行的隐式队列。该能力扩展现有 operation preview/apply 与审计，不引入 Worker 或通用调度系统。

## Consequences

Apply 必须携带 preview 生成的 Sandbox Operation Precondition，至少包括 expected generation、expected revision 和会被中断的 Runtime Instance 集合；任何变化都使 preview 失效。Reconcile 或 Reset 的 preview 必须明确列出运行中实例，apply 才能停止它们。Lifecycle operation 期间阻止新的 start/restart Runtime Command，但允许查询以及 operation 自身所需的 stop。Controller 崩溃后将未完成 operation 标记为 interrupted，先通过资源发现重建 Observed Sandbox State，绝不盲目续跑破坏性步骤；相同 operation/request identity 的重试必须幂等。
