---
status: accepted
---

# Ship one local docker-compatible driver first

Sandbox 的持久化项目契约保持标准 `devcontainer.json`，领域模型不包含 Docker 专属配置。实现通过 Dev Container Driver 与窄的 Container Engine Adapter 隔离执行技术。首版只交付本机 Docker-compatible driver：Dev Container CLI 负责配置解析、build、up 与 lifecycle command；engine adapter 负责 owned resource discovery、labels、volumes、loopback port publishing、Sandbox Agent Payload mount 和 bootstrap。

## Consequences

Docker Desktop、OrbStack、Colima 或其他本机实现仅在通过同一 Docker-compatible capability probe 时走相同代码路径，不按品牌分支。SSH/TCP remote Docker context 因不满足本地 bind mount、Host loopback 与 Host Gateway 假设而明确拒绝。Podman、Kubernetes、Firecracker 和远端 Sandbox Provider 不进入首版实现或验收。Docker CLI/API 不能散落到 Workspace、Runtime、日志或调试层；未来 Provider 实现相同 driver contract，不改变 Project、Sandbox、Runtime Command 或观测协议。
