---
status: accepted
---

# Inject the sandbox agent payload from the controller

Sandbox Agent 不写入项目镜像、不由 lifecycle command 安装，也不从 Sandbox 内联网下载。Controller Agent 根据 Sandbox 实际 OS/architecture 选择随 SuperDev 发布的 Sandbox Agent Payload，并委托 Container Engine adapter 将它只读供给到固定容器路径；Docker 实现使用只读 bind mount。Controller 再以 Sandbox Development User 启动该二进制。此供给机制属于 SuperDev 控制面，不进入 `devcontainer.json` 或 Sandbox Revision。

## Consequences

Sandbox Agent 启动握手必须报告版本、构建标识、当前可执行文件摘要和 capabilities，Controller 与期望 Sandbox Agent Build Identity 不一致时禁止新 Runtime Command。SuperDev 升级只需更新 Controller 所选 Payload 并重启 Sandbox Agent，不要求重建 Sandbox，Sandbox Agent State 与 Workspace State 保持不变。现有跨平台远端 Agent 构建产物和 health 版本入口应被扩展复用；不得新增项目侧 Agent 安装脚本或隐式下载通道。Container Engine adapter 可以改变 Payload 的传送方式，但不能把其所有权转交给项目配置。
