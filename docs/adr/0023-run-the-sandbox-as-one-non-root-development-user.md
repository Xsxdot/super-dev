---
status: accepted
---

# Run the sandbox as one non-root development user

首版 Sandbox Agent 与其启动的 Runtime Instance 都使用 Dev Container 解析出的 Sandbox Development User，默认不得以 root 运行。Sandbox Agent 二进制以只读方式供给，Sandbox Agent State 初始化为该用户可写，控制端口使用非特权端口。这样可以直接复用现有 `superdev-agent`、Language Runtime Provider 和 `process.Manager`，不新增 supervisor 用户、进程降权或用户切换子系统。

## Consequences

Sandbox Security Boundary 隔离 Workspace 与 Controller 主机及其他 Workspace，但不防御同一 Workspace 内的恶意代码；同 UID 的项目进程理论上可以影响或终止本 Sandbox Agent。生成的 Sandbox Definition 不加入 Docker Socket、host network、device、`privileged` 或 `seccomp=unconfined`，并保留容器引擎默认 seccomp。代码调试确需 `CAP_SYS_PTRACE` 等 capability matrix 明确支持的 Sandbox Security Capability 时，必须由 capability preflight 触发显式 preview/apply，并把能力加入 Sandbox Revision 与高风险 Trust 指纹。Capability matrix 标为 unsupported 的能力即使来自已有 Dev Container Definition 也继续拒绝，不能通过普通或高风险 Trust 绕过。
