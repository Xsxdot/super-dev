---
status: accepted
---

# Run a full SuperDev agent in each sandbox node

首版把每个 Sandbox 容器视为一个临时 Sandbox Node，并在其中安装和运行完整 Sandbox Agent。Controller Agent、远程节点 Agent 和 Sandbox Agent 运行完全相同的 `superdev-agent` 二进制，名称只表示拓扑角色，不引入新的 Agent 模式。Controller Agent 负责 Dev Container 生命周期和节点身份；Sandbox Agent 使用自身 Language Runtime Provider、process.Manager、调试能力和日志存储接管 Runtime Instance。Container Engine 不直接启动或终止应用 Service，因此不再需要非驻留 launcher。本 ADR 取代 ADR-0011 中由 Controller Agent 直接管理 Runtime Exec 的方案。

## Considered Options

拒绝由 Controller Agent 通过 Container Engine 直接管理 Service Exec，因为这会在容器边界外重复实现现有进程组、运行状态、日志和调试能力。轻量 Sandbox Worker 可以在未来替换完整 Sandbox Agent，但本期不承担其协议和可靠性复杂度。

## Consequences

现有 managed deployment 投影补充 Workspace、Runtime Instance、语言、容器内项目路径、readiness 和调试等现有 runtime 所需字段，并新增单一 Runtime Command API，支持 start、stop、restart 和 status。Agent 内部应抽取本地 handler 与远程 Runtime Command 共用的 RuntimeService，继续复用现有 Language Runtime Provider 和 process.Manager；不新增 Runtime Assignment Store 或另一套配置解析。Sandbox Agent 的完整控制面与本地存储开销在首版被接受；未来 Worker 只需兼容相同 Runtime Command、日志和调试契约。
