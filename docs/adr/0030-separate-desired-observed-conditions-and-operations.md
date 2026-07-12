---
status: accepted
---

# Separate desired, observed, conditions, and operations

Sandbox 状态由 Desired Sandbox State、Observed Sandbox State、Sandbox Condition 与可选 active Sandbox Lifecycle Operation 组合表达，不使用把所有维度交叉相乘的单一生命周期枚举。Desired state 只持久化 Workspace Execution Mode 与目标 Sandbox Revision；observed state 从 Container Engine、Agent health 和 Endpoint Binding 重建，presence 仅区分 absent、stopped、running 与 conflicted。

## Consequences

DefinitionResolved、TrustSatisfied、RevisionCurrent、ContainerReady、AgentReady、CapabilitiesSatisfied 与 EndpointsReady 等事实作为正交 condition 返回。Stale 表示 RevisionCurrent=false，不是独立 phase；preparing、reconciling 和 resetting 来自 active operation；failed 是 operation 终态，不能覆盖仍可观察或停止的旧 Sandbox。Sandbox Readiness 由全部执行前置 condition、current revision 和无冲突 operation 统一派生，新的 start/restart Runtime Command 只在 ready 时允许，查询及可达情况下的 stop 保持可用。API 返回统一 `summary_status` 和 `allowed_actions`，UI 与 Coding Agent 不各自重复推导。
