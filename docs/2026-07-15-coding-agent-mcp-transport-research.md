# Coding Agent MCP transport 调研与 SuperDev 架构建议

> 调研时间：2026-07-15。外部事实仅采用 MCP 官方规范、各产品官方文档或官方源码；下列链接访问日期均为 2026-07-15。产品结论针对本机 CLI、IDE、桌面端；云端/容器内的 `127.0.0.1` 指向其自身环境，不能直接访问宿主机 SuperDev。

## 结论

可以把 MCP server 直接放进常驻的 `superdev-agent`，在 `http://127.0.0.1:57017/mcp` 提供 Streamable HTTP。所调查的七类主流 Coding Agent 都能直连这类 URL，因此不会再由客户端启动 `superdev-mcp`，当前“Codex 启动 helper → macOS 因签名无效 SIGKILL → `tools/list` 前断链”的故障类别会从主路径消失。

但必须区分“代码放在哪里”和“进程怎么运行”：若只是把 MCP stdio 子命令编进 `superdev-agent`，再让 Codex 执行 `superdev-agent mcp-stdio`，客户端仍会 spawn 子进程，签名、路径、cwd、环境和生命周期链路仍在；只有连接已运行 daemon 的 Streamable HTTP 才真正去掉 helper。

推荐目标是 **C：agent 内单一 MCP core，Streamable HTTP 为默认入口，另留极薄 stdio adapter 作兼容**。也就是架构选 C、日常主路径用 B；迁移稳定后可删除当前承载完整工具 core 的独立 `superdev-mcp` 形态，而不是仓促取消全部 stdio 兼容能力。

## 七家官方支持情况

| Coding Agent | transport 与配置 | localhost、认证、工具刷新 | spawn 与调用方上下文 |
|---|---|---|---|
| OpenAI Codex | stdio、Streamable HTTP；HTTP 配置支持 URL、bearer token 环境变量及静态/环境 header。[官方源码](https://github.com/openai/codex/blob/main/codex-rs/config/src/mcp_types.rs) | URL 可以指向 localhost；配置变更至少应按“新建任务/重启客户端”验收，不能假设所有界面都热刷新。 | stdio 由 Codex spawn，可配 `cwd`；源码明确拒绝给 Streamable HTTP 配 `cwd`，因此 SuperDev 必须以显式项目/工作区标识为主，不能依赖进程目录。 |
| Anthropic Claude Code | 推荐 Streamable HTTP；另支持 stdio，legacy SSE 已弃用；local/project/user scope。[官方文档](https://code.claude.com/docs/en/mcp) | localhost URL、header、OAuth 均支持；明确支持 `list_changed` 自动刷新，HTTP/SSE 自动重连。 | stdio spawn 并获得 `CLAUDE_PROJECT_DIR`；HTTP 不 spawn。支持标准 `roots/list`，可辅助识别工作区，但工具参数仍应显式。 |
| Cursor | stdio、SSE、Streamable HTTP；项目 `.cursor/mcp.json` 或全局配置。[官方文档](https://docs.cursor.com/context/model-context-protocol) | Streamable HTTP 可用于本机或远端并支持 OAuth；可启停 server/tool，未承诺跨版本一致的动态热刷新。 | stdio 由 Cursor 管理；HTTP 不 spawn。`${workspaceFolder}` 是配置插值，不等于每次 HTTP 调用都携带 cwd。 |
| VS Code + GitHub Copilot | VS Code 支持 stdio、HTTP、SSE，并优先 HTTP 后回退 SSE；`.vscode/mcp.json` 或用户配置。[VS Code 官方文档](https://code.visualstudio.com/docs/agents/reference/mcp-configuration) Copilot CLI 同样支持 stdio、Streamable HTTP、legacy SSE。[GitHub 官方文档](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers) | 支持 localhost、headers、OAuth；VS Code 可 restart server / reset cached tools，Copilot CLI 新配置可立即可用。 | stdio spawn，VS Code 可配 `cwd`；HTTP 不 spawn且无天然 workspace cwd。 |
| Gemini CLI | `command`=stdio、`url`=SSE、`httpUrl`=Streamable HTTP；用户/项目 `settings.json`。[官方文档](https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md) | `httpUrl` 可指向 localhost；支持 headers 与 OAuth。文档描述启动时发现工具，未保证 `list_changed` 热刷新。 | stdio spawn，可配 env/cwd；HTTP 不 spawn，项目归属应走工具参数。 |
| Windsurf Cascade | stdio、Streamable HTTP、SSE；`~/.codeium/windsurf/mcp_config.json`。[官方文档](https://docs.devin.ai/desktop/cascade/mcp) | remote URL 支持 headers/OAuth、工具启停；未承诺动态工具热刷新。 | stdio spawn；remote 不 spawn。配置无可靠的 per-call cwd 契约。 |
| OpenCode | `type: local`（stdio）或 `type: remote`；`opencode.json(c)`。[官方文档](https://opencode.ai/docs/mcp-servers) | remote 支持 URL、headers、OAuth；源码先尝试 Streamable HTTP，再回退 SSE，并处理 `ToolListChangedNotification`。[官方源码](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/mcp/index.ts) | local command 会 spawn，可配 cwd/env；remote 不 spawn。官方源码声明 Roots capability，但 SuperDev 仍不应只靠 Roots。 |

共同结论：七家官方资料都表明产品不是“只能 stdio”，其当前配置模型可以表达 `127.0.0.1:57017/mcp` 这类 Streamable HTTP endpoint。这里是能力调研，不等于已对用户机器上的每个具体版本做过真实握手；上线前仍需按版本矩阵实测。差异主要在 legacy SSE、工具热刷新和 Roots，而不是 Streamable HTTP 的基本配置能力。

## A / B / C 比较

| 方案 | 兼容性与收益 | 主要代价 | 判断 |
|---|---|---|---|
| A. 独立 stdio helper | 所有客户端都支持，兼容面最广 | 每次由客户端 spawn；继续承担签名、路径、沙箱、cwd、环境和进程退出风险；helper 还重复协议/转发职责 | 只适合作为旧版兜底，不应继续做默认路径 |
| B. agent 直接暴露 remote MCP | 七家主流客户端均可直连；彻底移除默认 helper 链，daemon 生命周期统一 | HTTP 没有隐含 cwd；需补齐协议会话、并发、安全与重连 | 主流客户端的正确默认连接 |
| C. agent 内 MCP core + HTTP 主入口 + 极薄 stdio adapter | B 的稳定性，加上 A 的长尾兼容；工具定义和业务调用保持单一来源 | fallback adapter 仍会被 spawn，仍须正确签名和打包 | **推荐目标架构** |

MCP 当前标准 transport 是 stdio 与 Streamable HTTP；后者取代旧 HTTP+SSE，并要求单一 endpoint 处理 POST/GET。[MCP transport 规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports) 工具变化可通过 `tools.listChanged` 与 `notifications/tools/list_changed` 通知，但客户端支持并不一致。[MCP tools 规范](https://modelcontextprotocol.io/specification/2025-11-25/server/tools) cwd 不是 transport 字段；Roots 是可选能力。[MCP Roots 规范](https://modelcontextprotocol.io/specification/2025-11-25/client/roots)

## SuperDev 代码现状

以当前 checkout 为准：[`agent/cmd/superdev-mcp/main.go`](../agent/cmd/superdev-mcp/main.go) 是很薄的入口，读取 `SUPERDEV_AGENT_URL` 后运行 stdio；但该进程会装载完整工具注册表，并通过 [`agent/mcp/client.go`](../agent/mcp/client.go) 的 REST adapter 调 agent；[`agent/mcp/protocol.go`](../agent/mcp/protocol.go) 手写 `initialize/tools/list/tools/call`。桌面侧 legacy 核心 [`desktop/src-tauri/src/mcp_install.rs`](../desktop/src-tauri/src/mcp_install.rs) 处理 Codex、Claude Code、Cursor，而 [`desktop/src-tauri/src/mcp_install/connectors.rs`](../desktop/src-tauri/src/mcp_install/connectors.rs) 当前实际注册七个内置 Connector：Claude Code、Codex、Cursor、OpenCode、OpenClaw、Hermes、Kimi Code；它们目前都以 stdio helper 为连接材料。也就是说工具业务已集中在 MCP 包，但生产连接仍是“客户端 → helper → REST → agent”。

市场调研中的七家与仓库当前七个内置 Connector 不是同一集合。Codex、Claude Code、Cursor、OpenCode 可以作为首批 HTTP 迁移对象；OpenClaw、Hermes、Kimi Code 必须分别核实其当前版本的 remote transport 后再切换。VS Code/Copilot、Gemini CLI、Windsurf 则属于后续新增 Connector 或通用 HTTP 配置材料的覆盖范围。

内嵌后不应复制第二套工具表，也不宜长期让 agent 通过 localhost REST 调自己。应保留 transport-neutral tool registry，给它注入进程内 application facade；HTTP 与 stdio 只负责协议适配。

## 推荐架构与迁移门槛

1. 在常驻 agent 增加 `/mcp`，优先采用 [MCP 官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk)，兼容客户端仍在使用的 `2025-06-18` 与最新稳定 `2025-11-25`，不要继续硬编码单一版本。
2. MCP 只在桌面本地 controller 启动参数下启用并绑定 loopback；同一个 `superdev-agent` 二进制也用于远端 node，不能仅凭“代码在同一进程”就让远端安装自动获得 `/mcp`。严格校验 `Origin`，不得直接复用宽松 `Access-Control-Allow-Origin: *`；如允许非本机访问，必须 TLS + bearer/OAuth。规范明确要求 Origin 校验并建议本机绑定 `127.0.0.1`。[安全要求](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#security-warning)
3. HTTP 多客户端没有天然 cwd。所有关键工具继续显式传 `project_id/workspace_id/service_id`；Roots 只能辅助，不作唯一契约。若保存会话选择，必须按 `MCP-Session-Id` 隔离。
4. 先把桌面 Connector 契约从只有 `command + agent_url` 的 stdio 形态改成显式枚举 `Stdio` / `StreamableHttp`，状态中记录 transport 与 endpoint，避免双重真值。安装器采用“单写新格式、双读兼容”：新安装只生成一个 HTTP `superdev` 条目，状态、更新与卸载同时识别旧 stdio 和新 HTTP；不要同时写两个同名活动 server。首批迁移四个已核实 Connector，其余逐个验证。保留薄 adapter，待真实版本矩阵和企业策略验证后再决定是否完全移除。
5. 上线门槛：覆盖 initialize/initialized、tools/list/call、`application/json` 与 `text/event-stream` 响应模式、协议协商、Origin/鉴权、两个客户端并发隔离、agent 重启重连、旧配置迁移；分别实测七家“新任务可见工具”。工具 schema 尽量稳定，不能把 `list_changed` 当所有客户端都会处理。

最终判断：**可以直接放进 `superdev-agent`，而且应当这么做；推荐 C，默认走 B。** 这才是对本次 Codex helper 签名故障的根治，而不是重新签一次名后继续依赖同一条易碎启动链。
