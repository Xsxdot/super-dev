---
status: accepted
---

# Separate workspace state from shared download caches

Sandbox 存储分为 Workspace State、Tool Download Cache 和 Sandbox Artifact。依赖安装结果、虚拟环境、原生模块和增量构建状态属于 Workspace State，按 Workspace 隔离；只有能够重新下载且对并发和兼容性安全的 Tool Download Cache 才能跨 Workspace 共享；运行二进制和调试临时产物作为 Sandbox Artifact 独立管理。

## Consequences

Agent 在生成 Sandbox Definition 时使用 `customizations.superdev` 声明逻辑 managed mount，SuperDev 再按 Workspace、容器平台、架构和工具链兼容性生成本机 volume 身份，不能把机器相关 volume 名提交到 Git。锁文件变化由安装生命周期同步现有 Workspace State；工具链或 ABI 不兼容时轮换 Workspace State。宿主机的依赖目录、虚拟环境和语言缓存不得作为这些存储的来源。
