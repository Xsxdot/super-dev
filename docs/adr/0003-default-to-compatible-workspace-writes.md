---
status: accepted
---

# Default to compatible workspace writes

容器型 Sandbox 默认采用 Compatible Write Policy：Sandbox 可以修改 Workspace 源码，但平台相关依赖、缓存和编译产物必须保存到 Sandbox 专属存储。SuperDev 另提供 Strict Write Policy，将 Workspace 以只读方式提供给 Sandbox。默认只读会破坏格式化、代码生成、迁移生成和测试快照等常见开发流程，因此不作为兼容开源项目的默认值。

## Consequences

Agent 生成 Sandbox Definition 时必须识别并隔离 `node_modules`、虚拟环境、语言缓存和构建产物等平台相关路径。Compatible 模式下，Sandbox 生命周期命令造成的 Workspace 修改必须可观察；Strict 模式下，任何需要修改 Workspace 的任务都应明确失败，而不是临时放宽权限。
