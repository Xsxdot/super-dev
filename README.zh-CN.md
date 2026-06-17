<p align="center">
  <img src="./docs/assets/readme/superdev-logo-v5-launch.svg" width="96" alt="SuperDev logo" />
</p>

<h1 align="center">SuperDev</h1>

<p align="center">
  <strong>让 AI 和你共享同一份运行态：服务、日志、部署、审批。</strong>
</p>

<p align="center">
  <a href="https://gosuper.dev/"><strong>官网 gosuper.dev</strong></a> ·
  <a href="#为什么是-superdev">为什么</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="https://gosuper.dev/#demo">演示</a> ·
  <a href="#核心架构">架构</a> ·
  <a href="./README.md">English</a>
</p>

<p align="center">
  <a href="https://gosuper.dev/">
    <img alt="SuperDev runtime console" src="./docs/assets/readme/screenshot-zh.png" width="760" />
  </a>
</p>

> **观看完整流程：** 选择 AI 工具、安装 MCP、新增主机，让 AI 创建项目、服务、环境、流水线和部署，完成必要审批，并得到共享运行态总览：[gosuper.dev/#demo](https://gosuper.dev/#demo)。

<p align="center">
  <img alt="Platform: macOS first" src="https://img.shields.io/badge/platform-macOS%20first-111827" />
  <img alt="Tauri" src="https://img.shields.io/badge/Tauri-2.x-24C8DB" />
  <img alt="Vue" src="https://img.shields.io/badge/Vue-3-42b883" />
  <img alt="Go" src="https://img.shields.io/badge/Go-agent-00ADD8" />
  <img alt="MCP" src="https://img.shields.io/badge/MCP-ready-7C3AED" />
  <img alt="Local first" src="https://img.shields.io/badge/local--first-yes-16A34A" />
  <a href="https://github.com/Xsxdot/super-dev/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Xsxdot/super-dev/actions/workflows/ci.yml/badge.svg" /></a>
</p>

## 为什么是 SuperDev

AI 编程工具已经能读代码、改代码、跑命令，但代码协作只解决了“AI 知道仓库里有什么”。真正开发时，最难的是“AI 不知道你此刻正在运行什么”：哪些服务已经启动，哪个端口正在被使用，哪段日志对应当前功能，哪次 pipeline 刚刚发布，哪个远端 deployment 正在出错。

如果 AI 看不到这些运行态，它就会另起服务、抢占端口、制造一套影子环境；每次对话都像从零开始，无法持续追踪某一项功能从本地调试、日志变化、部署流水线到线上错误的完整生命线。

SuperDev 把本地服务、远端主机、日志、pipeline、ingress 和审批上下文收敛成一份本地优先的事实源，并通过 MCP 暴露给 Claude Code、Codex、Cursor 等智能体。AI 不再站在代码仓库外猜测，而是和你进入同一个真实开发现场。

## 第一目标：与 AI 共享运行态

SuperDev 不是诊断工具，也不是 AI 运维遥控器。它的第一目标是建立一种真正的协作：让开发者和 AI 在同一份运行态上工作。

这意味着 AI 先看见你已经启动的服务，而不是再启动一套；先读取同一段日志，而不是让你复制粘贴；先理解当前 deployment、pipeline、ingress 和审批上下文，而不是把线上错误当成孤立文本来猜。

当 AI 和人共享运行态，协作才会连续：一个功能可以被持续跟踪，一次线上错误可以沿着服务、日志、部署和入口状态被追到源头，真实环境里的操作也可以在预检、审批、一次性 token 和审计之下完成。

## 高光功能

两件核心的事——**共享运行态**和**安全操作**——是 SuperDev 区别于代码层工具的根本；其余能力都围绕它们展开。

### 🤝 共享运行态，不抢占用户服务

- AI 先观察已有服务、端口、日志和部署状态，再决定是否需要请求操作。
- 避免 AI 另起一套 shadow environment，减少端口竞争、重复进程和状态分叉。
- 同一项功能可以从本地运行、日志变化、pipeline run、远端 deployment 到 ingress 持续追踪。
- 线上错误不再只是贴给 AI 的一段文本，而是能被放回服务、部署、日志和入口上下文里共同处理。

### 🔒 AI 安全操作真实环境

- `superdev-mcp` 随桌面端分发，默认连接本机 agent：`http://127.0.0.1:57017`。
- 内置 SuperDev skill，指导 AI 按“先建立全局视野、再采证、再推理、最后安全执行”的顺序使用工具。
- 运行态写操作直接调用 `start/stop/restart`；需要审批时 MCP 默认等待桌面端批准，并用一次性 token 自动续跑。
- 审批 token 与具体 operation fingerprint 绑定，短期有效、一次性使用、不可换目标复用。
- 审批、拒绝、执行和失败都会进入本地审计记录。

### 围绕这两点的能力

| 能力 | 说明 |
| --- | --- |
| **多服务运行态控制台** | 统一查看本地进程、Launchd、systemd、Docker、远程主机上的 service / deployment；支持 managed control 与 monitor-only 两种模式；桌面端与 MCP 共享同一份运行态模型。 |
| **日志聚合与诊断** | 实时 / 历史日志、跨服务搜索、上下文查看、规则过滤、面板分栏、同步录制、书签区间、折叠重复日志。诊断只给确定性证据，根因推理留给 AI。 |
| **生产级 pipeline 底座** | DAG pipeline、模板组合、变量系统、artifact、run history、run log replay；内置 Go / Node / Python / Java / Rust / PHP / Vue+Go 模板；systemd 采用 release/current 结构，回滚复用同一条路径。 |
| **Ingress 声明式入口** | pipeline 管“反复投递产物”，Ingress 管“长期存在的入口状态”：域名、DNS、反向代理、HTTPS、证书托管。支持 nginx、manual DNS、Cloudflare、Aliyun、ACME DNS-01 与 orphan detection。 |

### 零操作 onboarding

- 首次打开引导页选择 Claude Code、Codex 或 Cursor。
- 一键安装 MCP 连接和 SuperDev 使用指南 skill。
- 自动落地 `superdev-sample` 示例项目。
- 复制一段提示词给 AI，即可体验“看日志 -> 触发审批 -> 用户批准 -> MCP 自动继续执行”的闭环。

## 快速开始

### 从源码运行

```bash
git clone https://github.com/Xsxdot/super-dev.git
cd super-dev
cd desktop
pnpm install
pnpm tauri dev
```

> 首个公开 release 打包完成后，这里会补充 macOS 应用下载方式。当前 README 不提供未验证的下载链接。

### 体验 AI 安全演示

1. 打开 SuperDev。
2. 在 onboarding 中选择 Claude Code、Codex 或 Cursor。
3. 点击安装 MCP 连接。
4. 复制页面给出的提示词并发给 AI。
5. 当 AI 请求重启示例服务时，在 SuperDev 的“操作审批”中批准。
6. MCP 自动获取一次性 approval token 并继续执行；AI 再次读取日志解释 WARN/ERROR。

## 核心架构

```mermaid
flowchart TB
    AI["Claude Code / Codex / Cursor"] --> MCP["superdev-mcp"]
    MCP --> Agent["Local SuperDev Agent (Go)"]
    Desktop["Desktop UI (Tauri + Vue)"] --> Agent
    Agent --> Runtime["Runtime Control"]
    Agent --> Logs["Logs & Diagnostics"]
    Agent --> Pipelines["Pipelines & Artifacts"]
    Agent --> Ingress["Ingress / DNS / HTTPS"]
```

SuperDev 的关键边界是：local agent 是运行态网关和事实源。MCP 不绕过 agent 直接读写配置、SQLite、进程或远程机器；安全门禁也在 agent 层强制执行，而不是只靠提示词约束 AI。

## 示例与模板

`examples/` 提供用于验证内置 pipeline 模板的最小项目：

| Example | Template | Runtime |
| --- | --- | --- |
| `go-http` | `go-binary-build` | Go binary |
| `node-http` | `node-standard-build` | Node |
| `python-http` | `python-standard-build` | Python |
| `java-springboot` | `java-maven-build` | Java Spring Boot |
| `rust-http` | `rust-cargo-build` | Rust binary |
| `php-http` | `php-standard-build` | PHP built-in server |
| `vue-go-combined` | `vue-go-combined-build` | Go serving Vue dist |
| `mcp-log-lab` | runtime/log diagnostics fixture | Go command services |

Ingress 示例位于 `examples/ingress/`，覆盖 manual DNS、Cloudflare、Aliyun、nginx 和 TLS 配置。

## 当前状态与路线图

SuperDev 正处于第一版开源发布前夜，当前主要面向 macOS 桌面端和本地优先工作流。

- 已有：Tauri 桌面端、Go local agent、MCP server、SuperDev skill、多服务日志、operation approvals、pipeline 模板、ingress 子系统、零操作 onboarding。
- 近期：更完整的 release 打包、正式 README 截图、更多 pipeline 模板、更稳的远端 agent / tunnel 体验、更完整的发布演示。
- 原则：本地优先，不把控制面强行放到云上；AI 可以参与操作，但写操作必须可预检、可批准、可审计。

## 平台支持

| 平台 | 本地桌面端 | 远端 agent 安装目标 | Go/Python/Rust/C/C++ 调试 | Node 调试 | JVM 调试 |
| --- | --- | --- | --- | --- | --- |
| macOS (`darwin`) | 支持，随 Tauri sidecar 打包。 | `superdev-agent-darwin-amd64` 与 `superdev-agent-darwin-arm64`。 | 安装对应语言调试器后走默认 attach 流程。 | experimental，通过 `SIGUSR1` 打开 inspector。 | experimental，需自备或配置 JVM adapter。 |
| Linux | `desktop-linux` CI 覆盖打包。 | `superdev-agent-linux-amd64` 与 `superdev-agent-linux-arm64`。 | 安装对应语言调试器后走默认 attach 流程。 | experimental，通过 `SIGUSR1` 打开 inspector。 | experimental，需自备或配置 JVM adapter。 |
| Windows | `desktop-windows` CI 覆盖打包，并使用 `.exe` sidecar。 | `superdev-agent-windows-amd64.exe`。 | 对应调试工具链可用时走默认 attach 流程。 | experimental，使用预注入 `--inspect=0`，不依赖 Unix signal。 | experimental，需自备或配置 JVM adapter。 |

## 开发

```bash
# Desktop
cd desktop
pnpm install --frozen-lockfile
pnpm build          # 前端类型检查 + vite 构建（不打包桌面端）
pnpm test           # 前端单元测试（vitest）
pnpm tauri build    # 打包 macOS 桌面应用（产出安装包，不会启动应用）

# Agent
cd agent
go test ./...
```

> `pnpm build` 只构建前端资源；要打包完整的 macOS 桌面应用用 `pnpm tauri build`，想直接运行起来看效果用 `pnpm tauri dev`。

Tauri 构建会通过 `desktop/scripts/build-agent.sh` 打包 sidecar 二进制：`superdev-agent`、`superdev-mcp`、`superdev-sample`。

## 开源治理

- 贡献指南：[CONTRIBUTING.md](./CONTRIBUTING.md)
- 安全报告：[SECURITY.md](./SECURITY.md)
- 版本与发布：[docs/release.md](./docs/release.md)
- 变更日志：[CHANGELOG.md](./CHANGELOG.md)

## 贡献

欢迎贡献 pipeline 模板、运行时适配器、日志诊断能力、ingress provider、文档和示例项目。提交 PR 前请尽量附上可复现的验证命令；涉及运行态写操作时，请保持 preview、approval 和 audit 的安全边界。

## License

查看 [LICENSE](./LICENSE)。
