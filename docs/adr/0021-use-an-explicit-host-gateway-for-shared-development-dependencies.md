---
status: accepted
---

# Use an explicit host gateway for shared development dependencies

Sandbox 使用 `host.superdev.internal` 作为访问 Controller 主机上 Shared Development Dependency 的稳定 Host Gateway，由 Container Engine adapter 映射到具体宿主网关。SuperDev 不自动把 runtime 配置中的 `localhost` 或 `127.0.0.1` 改写为 Host Gateway；Agent 只能检测疑似 Host 依赖、给出预警，并通过现有 preview/apply 配置变更显式修改。

## Consequences

Sandbox 环境注入 `SUPERDEV_HOST_GATEWAY=host.superdev.internal`，Prepare 后由 Sandbox Agent 对声明的共享依赖执行网络 probe，并区分服务未启动、监听地址不可达、网关解析失败和防火墙等原因。局域网或远程依赖保持原地址。Engine adapter 可以用 Docker `host-gateway`、Podman 对应机制或未来实现解析该名称，项目配置不绑定具体容器产品。
