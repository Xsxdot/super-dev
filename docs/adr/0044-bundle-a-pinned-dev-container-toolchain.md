---
status: accepted
---

# Bundle a pinned dev container toolchain

SuperDev 在桌面包构建阶段使用官方安装产物准备固定版本的 Dev Container CLI 与配套 Node runtime，记录版本和 SHA-256，并只打包当前 Host target 所需的 Dev Container Toolchain。用户机器不执行在线安装脚本，也不依赖全局 Node、npm、VS Code 或用户 PATH 中的 `devcontainer`；生产路径只调用内置 bundle，开发测试 override 必须显式。

## Consequences

Controller health 暴露 toolchain version、digest 与 capability probe。升级先通过兼容 smoke；只有 effective resolved sandbox inputs 改变才产生新 Sandbox Revision，不能仅因 bundle 文件路径变化标 stale。Features 使用 Dev Container Lockfile：已有 lockfile 以 frozen 模式读取，缺失或更新必须由 config preview 生成 diff 并 apply 到 Workspace，prepare 不允许 CLI 隐式写文件。第三方 CLI、Node runtime 许可证和 notices 随包发布，build pipeline 校验 artifact digest。
