# SuperDev 跨平台重写设计文档

**日期**：2026-05-20  
**状态**：已确认，待实现

---

## 背景与目标

SuperDev 当前是 macOS 专属的 menubar 应用（Swift + SwiftUI + AppKit），核心功能是管理本地微服务进程、收集日志、提供 MCP 接口给 Claude Code。

重写目标：
1. **开源，支持全平台**：Windows、Linux、macOS 均可使用
2. **支持服务器部署**：Go agent 可以 headless 运行在服务器上，本地客户端按需连接拉取日志、控制服务
3. **预留多节点扩展**：架构上为未来连接多台服务器、自动化部署留出扩展点

---

## 架构概览

```
superdev/
├── agent/       # Go，核心逻辑（跨平台）
└── desktop/     # Tauri，桌面客户端（Win/Mac/Linux）
```

### 整体结构

```
┌─────────────────────────────────────┐
│  Tauri 桌面客户端                    │
│  ├── 启动时自动拉起本地 Go agent     │
│  └── Web 前端通过 HTTP/WebSocket     │
│      连接 agent（本地或远程同接口）   │
└───────────────┬─────────────────────┘
                │ HTTP REST + WebSocket
┌───────────────▼─────────────────────┐
│  Go agent（单二进制，跨平台）         │
│  ├── 读配置文件，管理服务进程         │
│  ├── 收集 stdout/stderr，存 SQLite   │
│  ├── 暴露 HTTP API + WebSocket 日志流 │
│  └── 预留远程节点连接接口            │
└─────────────────────────────────────┘
```

**关键设计原则**：
- Go agent 是完全独立的跨平台二进制，本地和服务器运行同一个二进制
- Tauri 客户端连接本地 agent 和连接远程 agent 的方式完全一致（统一接口）
- 本地主 agent 不要求常驻，服务器子 agent 自己持久化日志，客户端按需连接拉取

---

## Go Agent 设计

### 配置文件格式

兼容现有 SuperDev 配置格式（VS Code launch.json 兼容），存放在项目根目录。

### 进程管理

- 读取配置，启动/停止/重启服务进程
- 捕获 stdout/stderr，逐行解析写入 SQLite
- 日志保留策略：默认 7 天，可配置
- 内存缓冲：最多保留最近 8000 条在内存中，超出从 SQLite 读取

### 日志存储

使用 SQLite 持久化，支持：
- 按 runId 区分每次启动会话
- 按 serviceId、runId 查询，不做关键词过滤（过滤在客户端做）
- 分页游标查询（`before` cursor 基于 log ID）

### 通信协议

- **REST HTTP**：服务控制、配置管理、历史日志查询
- **WebSocket**：实时日志流推送，支持过滤参数

---

## API 接口

### 服务管理

```
GET    /api/services                  # 列出所有服务及状态
POST   /api/services/{id}/start       # 启动服务
POST   /api/services/{id}/stop        # 停止服务
POST   /api/services/{id}/restart     # 重启服务
```

### 项目与配置

```
GET    /api/projects                  # 列出所有项目
POST   /api/projects                  # 添加项目（传配置文件路径）
DELETE /api/projects/{id}             # 移除项目
GET    /api/projects/{id}/rules       # 获取项目级日志过滤规则
PUT    /api/projects/{id}/rules       # 更新过滤规则（全量替换，持久化到配置文件）
```

### 日志查询

**过滤完全在客户端执行**，agent 只返回原始日志。客户端维护两层独立过滤：
- **项目级规则（LogRule）**：从 agent 加载后缓存在客户端，随时 enable/disable 立即生效
- **面板临时 Chip 过滤**：用户在面板输入框输入的临时关键词，纯 UI 状态，不持久化

```
GET  /api/logs?service={id}&run={runId}&limit={n}&before={cursor}
     # 分页查询历史日志，before 为上一页最小 log ID，返回原始未过滤日志

WS   /ws/logs?service={id}
     # 实时日志流，连接后持续推送新日志，不做任何过滤
```

### 书签与同步组

书签和同步组完全在客户端（Vue 前端）维护，agent 不感知这两个概念：
- **书签**：客户端记录开始/结束时间点，从本地已过滤的日志中截取对应片段，导出时格式化当前展示内容
- **同步组**：客户端协调多个面板同时触发书签开始/结束，时间对齐，合并导出时拼接各面板已过滤的书签片段

无需任何 agent API。

---

## 数据模型

### LogEntry

```go
type LogEntry struct {
    ID        int64     `json:"id"`
    ServiceID string    `json:"service_id"`
    RunID     string    `json:"run_id"`
    Timestamp time.Time `json:"timestamp"`
    Level     string    `json:"level"`    // INFO / WARN / ERROR / DEBUG
    Message   string    `json:"message"`
    Stream    string    `json:"stream"`   // stdout / stderr
}
```

### Service

```go
type Service struct {
    ID        string `json:"id"`
    ProjectID string `json:"project_id"`
    Name      string `json:"name"`
    Status    string `json:"status"`      // stopped / starting / running / failed
    PID       int    `json:"pid,omitempty"`
    Command   string `json:"command"`
    WorkDir   string `json:"work_dir"`
    Required  bool   `json:"required"`    // 必须启动，强制勾选不可取消
    Order     int    `json:"order"`       // 启动顺序，相同 order 并行，值小的先启动；不填默认 0（全部并行）
    EnvFile   string `json:"env_file,omitempty"`
    Env       map[string]string `json:"env,omitempty"`
}
```

### Project

```go
type Project struct {
    ID                 string    `json:"id"`
    Name               string    `json:"name"`
    RootPath           string    `json:"root_path"`
    Services           []Service `json:"services"`
    SelectedServiceIDs []string  `json:"selected_service_ids"` // 用户勾选的待启动服务，持久化到配置文件
}
```

**启动逻辑**：点击"启动选中"时，合并 `required` 服务和 `SelectedServiceIDs` 中的服务，按 `order` 分组，同组并行启动，组间串行等待全部就绪后再启动下一组。

**UI 分组**（对应截图）：
- **必须启动**：`required: true`，强制勾选，不可取消
- **可选**：`required: false`，用户勾选状态持久化到 `SelectedServiceIDs`

### LogRule（项目级日志过滤规则，持久化到配置文件）

```go
type LogRule struct {
    ID       string   `json:"id"`
    Name     string   `json:"name"`
    Type     string   `json:"type"`     // "include" | "exclude"
    Keywords []string `json:"keywords"`
    Logic    string   `json:"logic"`    // "and" | "or"
    Enabled  bool     `json:"enabled"`
}
```

### LogBookmark / SyncGroupState（客户端状态，无对应 agent 数据模型）

书签和同步组是纯客户端概念，由 Vue 前端的状态管理（Pinia）维护，不涉及任何 agent 数据结构。

---

## Tauri 客户端设计

### 技术栈

- **Rust 壳**：Tauri，负责窗口管理、系统托盘、拉起本地 Go agent 进程
- **Web 前端**：Vue 3，复用现有 SuperDev 的 UI 逻辑和交互模式
- **通信**：前端直接通过 HTTP/WebSocket 访问 Go agent（本地 `localhost` 或远程 IP）

### 本地 agent 生命周期

- Tauri 启动时自动拉起打包好的 Go agent 二进制
- agent 监听本地固定端口（默认 `27017`，可配置）
- Tauri 退出时负责关闭本地 agent 进程

### 面板布局

保留现有 SuperDev 的多面板分栏设计，面板状态（布局、panelId、绑定服务）由前端本地维护，不存到 agent。

---

## 扩展点预留

### 多节点连接

当前设计中，客户端只连接一个 agent（本地或远程）。未来扩展为多节点时：
- 客户端维护一个 agent 列表（地址 + token）
- 并发连接多个 agent，聚合展示服务状态和日志
- agent API 接口不需要改变，客户端层做聚合

### 自动化部署

未来子 agent 之间传输构建文件和配置文件时，在现有 agent HTTP 接口上扩展文件上传接口即可，核心进程管理和日志收集逻辑不受影响。

---

## 现有功能迁移对照

| 现有 Swift/macOS 功能 | 重写后对应实现 |
|---|---|
| `ProcessRunner` / `ProcessManager` | Go `os/exec` 进程管理 |
| `LogEngine` / `LogStore` (SQLite) | Go agent + SQLite (`database/sql`) |
| `LogFilter` 内存过滤 | 客户端（Vue 前端）实现，两层：项目级规则 + 临时 Chip |
| `LogRule` 项目级规则 | 随项目配置持久化，API 读写 |
| `LogBookmark` 内存书签 | 客户端 Pinia 状态，面板级管理，无 agent API |
| `SyncGroup` 同步组 | 客户端 Pinia 状态，协调多面板书签触发，无 agent API |
| `ControlSocketServer` (UDS) | agent HTTP REST 接口 |
| `MenuBarManager` + SwiftUI | Tauri 系统托盘 + Vue 前端 |
| `UserDefaults` 持久化 | agent 本地 JSON 配置文件 |
| `superdev-mcp` CLI | 保留，改为连接 Go agent HTTP 接口 |
