---
status: accepted
---

# Fail closed on unsupported dev container capabilities

首版 Dev Container Capability Matrix 只执行单容器 `image` 或 Dockerfile `build`，并支持锁定的 Features、development user、Workspace folder、受审查 mounts 与 environment、容器内 create/update/post-create/post-start commands、endpoint declarations，以及显式 Trust 的 `CAP_SYS_PTRACE`。所有敏感字段都进入 Sandbox Revision 与 Trust fingerprint，secret 在 preview 和日志中脱敏。

## Consequences

Compose 的 `dockerComposeFile`、`service`、`runServices` 因 sidecar 生命周期延后而拒绝。Host `initializeCommand` 会突破 Sandbox 边界，`postAttachCommand` 又没有对应 SuperDev 生命周期，两者首版均阻塞并给出迁移建议。`privileged`、host network/PID/IPC、device、Docker Socket、`seccomp=unconfined` 和未知 `runArgs` 明确拒绝；run args 只接受可解释的安全白名单。与 Workspace/Git mount、动态 loopback ports 或非 root 用户模型冲突的配置同样阻塞。Preflight 返回精确字段路径、原因和建议，不忽略、不 best-effort，也不回退 Host；Agent 生成配置只使用 supported subset。
