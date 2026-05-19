# SuperDev MCP Server 设计文档

**日期**：2026-05-19  
**状态**：已确认，待实现

---

## 背景与目标

SuperDev 是一个管理本地微服务进程、收集日志的 macOS menubar 工具。当前开发流程中，Claude Code 只能通过终端输出观察服务状态，存在两个痛点：

1. Claude Code 启动的终端进程与用户已启动的服务冲突
2. 用户启动的服务产生的日志，Claude Code 看不到

目标：让 Claude Code 通过 MCP 协议直接控制 SuperDev 管理的服务（重启/停止）、查询历史日志，从而完成「修改代码 → 重启服务 → 观察日志 → 诊断问题」的完整开发循环。

---

## 架构概览

```
┌─────────────────────────────────────────┐
│  SuperDev.app                           │
│                                         │
│  ┌──────────┐    Unix Domain Socket     │
│  │ AppCore  │◄──────────────────────┐   │
│  │(进程控制) │                       │   │
│  └──────────┘                       │   │
│                                     │   │
│  SQLite DB ──────────────────────┐  │   │
│  (LogStore)                      │  │   │
│                                  ▼  ▼   │
│  ┌────────────────────────────────────┐ │
│  │  superdev-mcp  (bundled CLI tool)  │ │
│  │  · stdio MCP server               │ │
│  │  · 读 SQLite 查日志               │ │
│  │  · 发 UDS 消息控制服务            │ │
│  └────────────────────────────────────┘ │
│            ▲                            │
└────────────┼────────────────────────────┘
             │ stdio (JSON-RPC 2.0)
    ┌─────────────────┐
    │   Claude Code   │
    └─────────────────┘
```

**两个新增组件**：

- **`superdev-mcp`**：独立 Swift CLI target，打包进 `SuperDev.app/Contents/MacOS/`，由 Claude Code 以 stdio 模式启动
- **`ControlSocketServer`**：SuperDev 主 app 内新增，监听 Unix Domain Socket，接收控制指令

---

## MCP 工具接口

### `list_services`
```
输入：无
输出：[{ project: String, service: String, status: String }]
```
返回所有项目下所有服务的当前状态（stopped / starting / running / failed）。

### `restart_service`
```
输入：target: String   // 格式 "项目名/服务名"
输出：{ ok: Bool, message: String }
```

### `stop_service`
```
输入：target: String   // 格式 "项目名/服务名"
输出：{ ok: Bool, message: String }
```

### `query_logs`
```
输入：
  service?:  String   // "项目名/服务名"，不填则查所有服务
  level?:    String   // "ERROR" | "WARN" | "INFO" | "DEBUG"
  keyword?:  String   // 消息体包含此字符串（大小写不敏感）
  limit?:    Int      // 默认 100，最大 500
  since?:    String   // ISO8601 时间戳，不填则返回最近 limit 条
输出：[{ timestamp: String, project: String, service: String, level: String, message: String }]
```
直接查询 SQLite，支持跨 run 历史查询。

---

## UDS 控制协议

**Socket 路径**：`~/Library/Application Support/SuperDev/control.sock`

**请求格式**（每次新建连接，一行 JSON）：
```json
{ "action": "list_services" }
{ "action": "restart_service", "target": "myapp/user-service" }
{ "action": "stop_service", "target": "myapp/user-service" }
```

**响应格式**（一行 JSON，写完关闭连接）：
```json
{ "ok": true, "data": [...] }
{ "ok": false, "error": "service not found: myapp/user-service" }
```

每次请求建立新连接，响应后关闭，不维持长连接。

---

## superdev-mcp 实现结构

单一 Swift CLI target，入口 `main.swift`，逻辑分三层：

```
MCPServer
  · readline 循环，解析 JSON-RPC 2.0
  · 处理 initialize / tools/list / tools/call
  · 路由到 ToolHandlers

ToolHandlers
  · listServices()    → 连 UDS，转发 list_services
  · restartService()  → 连 UDS，转发 restart_service
  · stopService()     → 连 UDS，转发 stop_service
  · queryLogs()       → 直接开 SQLite 连接，查 log_entries 表

SQLiteReader
  · 复用 GRDB（与主 app 共享 SPM 依赖）
  · LogEntry 模型在两个 target 间共享
  · DB 路径：~/Library/Application Support/SuperDev/logs.db
```

`superdev-mcp` 只绑定两个外部依赖：UDS socket（控制）和 SQLite 文件（日志），无需 HTTP，无需端口。

---

## SuperDev 主 app 改动范围

### 新增文件

**`AppCore/MCP/ControlSocketServer.swift`**

```swift
final class ControlSocketServer {
    init(core: AppCore)
    func start()
    func stop()
}
```

监听 UDS，用 `DispatchSource` 处理连接，收到请求后 `DispatchQueue.main.async` 回主线程调用 `AppCore` 现有方法（`restart()`、`stop()`、`projects`）。不引入新依赖。

### AppCore 修改

`AppCore.init()` 末尾增加：
```swift
controlServer = ControlSocketServer(core: self)
controlServer.start()
```

### SettingsView 新增 MCP 区块

在设置页面底部增加「MCP 集成」区块，显示：
- Socket 路径（只读，可复制）
- `superdev-mcp` 可执行文件路径
- 「复制 Claude Code 配置」按钮，点击后将以下 JSON 写入剪贴板：

```json
{
  "mcpServers": {
    "superdev": {
      "command": "/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"
    }
  }
}
```

---

## 不在本次范围内

- 认证/Token（本地 127.0.0.1 绑定，威胁模型不需要）
- HTTP SSE 模式
- 日志字段结构化提取（正则/JSON 解析）
- `start_service` 工具（restart 已覆盖启动场景）

---

## 实现里程碑

1. **SuperDev UDS 监听层**：`ControlSocketServer` + `AppCore` 接入，可用 `nc` 手动测试
2. **`superdev-mcp` CLI target**：MCP 握手 + `list_services` + `restart_service` + `stop_service`
3. **`query_logs` 工具**：SQLiteReader + 查询逻辑
4. **SettingsView MCP 区块**：路径展示 + 一键复制配置按钮
