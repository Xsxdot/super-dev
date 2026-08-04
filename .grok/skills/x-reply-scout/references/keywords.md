# x-reply-scout 关键词批次

准心：日志、端口、进程、dev server、重启、服务管控。  
通用过滤示例：`lang:en`、需要时 `-filter:links`、`since:YYYY-MM-DD`。

## Wedge 批次（优先，按序跑）

### 日志
- `"paste logs" (claude OR agent OR cursor OR codex)`
- `(logs OR "can't see" OR "cannot see") (claude OR "claude code" OR cursor) (agent OR multi OR service)`
- `"tee" (AGENTS.md OR "coding agent" OR claude)`
- `"agent observability" OR "agent can see" (logs OR runtime)`
- `(mcp) (logs OR logging) (debug OR agent OR claude)`

### 端口 / 进程 / dev server
- `(EADDRINUSE OR "port already" OR "address already in use") (claude OR cursor OR agent OR codex OR vite OR next)`
- `("zombie" OR "orphaned" OR "left running") (dev server OR node OR vite) (agent OR claude OR cursor)`
- `("which process" OR "what is using" OR "kill the") (port) (dev OR agent OR claude)`
- `(ports OR process OR pid) (claude code OR cursor OR codex) (manage OR control OR stuck)`

### 重启 / 假修复
- `"claude code" (restarted OR restart OR "dev server") (broke OR failed OR again OR still)`
- `"it says it fixed" OR "claims it fixed" OR "said it fixed"`
- `("didn't restart" OR "fake restart" OR "thought it restarted") (agent OR claude OR cursor)`
- `"background" (process OR task OR server) (codex OR "claude code") (control OR kill OR stop)`

### 服务管控 / 审批 / 本地 runtime
- `(agent OR claude) (restart OR deploy OR production) (approve OR approval OR permission OR dangerous)`
- `"local first" (agent OR mcp) (debug OR runtime OR logs)`
- `"mcp server" (debugging OR logs OR ports OR process)`

### 浏览器验证（次优先，闭环相关）
- `("chrome devtools mcp" OR "browser mcp" OR "playwright mcp") (debug OR agent)`
- `(agent OR claude) (browser) (login OR verify OR "network tab") (debug OR test)`

## 降权批次（仅 wedge 不够时补）
- `"vibe debugging"` / `"vibe coded" (bug OR broken)` — 仅当正文涉及 runtime/logs/ports
- `"ai can write code but"` — 仅当落到 debug/run 痛点
- `"root cause" (agent OR claude) min_faves:5` — 需扫正文是否 runtime

## 明确不搜 / 命中后丢弃
- 模型排行、benchmark 吵架
- 纯 prompt 工程、system prompt 分享
- 无具体失败场景的「AI 改变编程」

## 实战备注（跑几天后往这里补）

- 高转化原话：
- 浪费时间的假阳性词：
- 值得加进私密 List 的账号：
