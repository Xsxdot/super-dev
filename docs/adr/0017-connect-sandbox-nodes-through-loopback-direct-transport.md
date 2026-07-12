---
status: accepted
---

# Connect sandbox nodes through loopback direct transport

每个 Sandbox Agent 在容器内监听固定控制端口，Container Engine 将该端口动态发布到 Controller 主机的 `127.0.0.1`，Controller Agent 再把运行期地址注册为现有 DirectTransport 节点。不同 Workspace 复用相同容器内端口、使用不同动态 Host 端口；Sandbox Recreate 可以改变 Host 端口，但不改变 Sandbox Node 身份。

## Consequences

Sandbox Control Endpoint 只绑定 Host loopback，仍强制使用每节点独立随机 token；token 保存在 Controller 与 Sandbox Agent State，不进入 Workspace、Dev Container Definition 或日志。首版本机 loopback 通道关闭 TLS，避免为动态地址建立证书身份；Sandbox Agent 在容器内监听 `0.0.0.0` 以接收 publish 流量。运行期 Direct Address 不持久化到项目配置，也不新增 SSH tunnel 或 Sandbox 专用传输协议。
