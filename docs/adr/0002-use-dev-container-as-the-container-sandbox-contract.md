---
status: accepted
---

# Use Dev Container as the container sandbox contract

容器型 Sandbox 只使用标准 `devcontainer.json` 作为持久化的 Sandbox Definition。项目缺少该文件时，由 Agent 生成候选配置并通过 preview、审批和 apply 写入 Workspace；运行时不静默生成 fallback，也不在 `.superdev/config.yaml` 重复保存镜像、工具链或挂载配置。SuperDev 专属策略使用 `customizations.superdev`，解析结果只作为非持久化运行快照。

## Considered Options

拒绝并行维护 SuperDev 容器配置、临时 Generated Fallback 和 Raw Docker Sandbox 配置，因为三套入口会产生语义漂移并让 Agent 无法判断哪份配置是事实来源。Sandbox 领域边界仍保持执行技术中立；未来非容器 Provider 可以采用自己的配置契约，而不改变 Project、Workspace 或 Deployment。

## Consequences

首版容器 Sandbox 的执行依赖兼容 Dev Container 的容器引擎，但核心模型不依赖 Docker。创建或更新开发环境会成为一项可审阅、可提交 Git、受 SuperDev 安全门禁约束的配置变更。
