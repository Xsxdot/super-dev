---
status: accepted
---

# Declare platform state isolation in the dev container

平台相关依赖与增量构建状态通过 `customizations.superdev` 中的 Isolation Manifest 显式声明，不由 Controller 在运行时偷偷推断和挂载。Workspace-private path 由 Dev Container Driver 物化为以 Controller/Workspace/path digest 标识并打 ownership label 的私有 volume；实际 engine volume 名不写入 Git。Tool cache 声明只允许可重新下载或已验证兼容的内容，并按 OS、architecture、toolchain 与 cache kind 分 namespace。

## Consequences

Language Runtime Provider 输出 Node、Python、Go、Rust、Java/C++ 等结构化 Isolation Hint，Coding Agent 经 config preview/diff/apply 将其固化。对高置信度平台状态缺失声明时 preflight fail-closed，即使 Host 目录尚不存在也防止容器安装后污染源码挂载；低置信度只预警，不能自动创建 mount。嵌套 volume 在容器中遮蔽 Host 同名目录，从空的 Linux 状态开始，不复制、移动或删除 Mac 产物。Sandbox Reset 删除 Workspace-private state，Tool Download Cache 仍由独立策略治理。Isolation Manifest 属于 Sandbox Definition、Revision 与 Trust 输入。
