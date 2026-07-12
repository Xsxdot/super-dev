---
status: accepted
---

# Bind the host workspace source without sync

首版使用 Host-bound Workspace Source：Workspace 源码由本机 bind mount 直接提供给 Sandbox，Host Coding Agent 与 Sandbox Runtime 始终观察同一份文件。SuperDev 使用 active Provider Profile 的文件共享能力，不实现 Mutagen、rsync、volume-backed clone 或品牌特有同步分支。

## Consequences

Preflight 验证 mount 可用性，状态暴露文件共享 backend/capabilities，并提供按需 I/O/watch probe 区分文件不可见、watch 事件丢失和延迟过高。性能退化形成 IOPerformanceDegraded condition 与诊断，不伪装成 Runtime 故障；文件不可见或明确项目要求的 watch 机制不可用才阻止 readiness。Agent 可以 preview 项目级 watcher 配置，但不得自动全局启用高 CPU polling。大型 Mac 仓库可能仍受 bind 性能上限影响；未来只能通过独立 SynchronizedWorkspaceProvider 引入同步语义，不能偷偷改变 Compatible Write Policy。
