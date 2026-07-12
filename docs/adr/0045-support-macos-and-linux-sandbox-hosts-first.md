---
status: accepted
---

# Support macOS and Linux sandbox hosts first

首版 Sandbox Host Platform 正式支持 macOS amd64/arm64 与 Linux amd64/arm64，Container 目标为 Linux amd64/arm64，并按实际容器 architecture 选择 Sandbox Agent Payload。macOS 使用本机 Docker-compatible Provider；Linux 使用当前用户可访问并通过 capability probe 的本机 Unix socket 或 rootless-compatible endpoint。

## Consequences

Windows 桌面继续保留全部现有 Host/Remote 能力，但 Sandbox 返回结构化 `sandbox_host_platform_unsupported`，不能展示可执行启用操作。Windows Sandbox 需要单独解决 Windows/WSL2 path、engine ownership、bind executable permissions、Host Gateway、loopback publish 与 Agent Payload injection 后再进入支持矩阵。公共 Driver、Workspace 和 Runtime contracts 仍保持跨平台，代码与单测不得写死 `/Users`、Unix socket 或 POSIX permission，只是首版端到端验收不覆盖 Windows。
