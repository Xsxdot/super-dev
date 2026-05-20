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
- 按 serviceId、level、keyword 过滤查询
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

```
GET  /api/logs?service={id}&level={level}&keyword={kw}&limit={n}&before={cursor}
     # 分页查询历史日志，before 为上一页最小 log ID

WS   /ws/logs?service={id}&level={level}&keyword={kw}
     # 实时日志流，支持过滤，连接后持续推送新日志
```

### 书签

```
POST   /api/bookmarks/{panelId}/start   # 开始录制（绑定 serviceId）
POST   /api/bookmarks/{panelId}/end     # 结束录制
GET    /api/bookmarks/{panelId}/export  # 导出为纯文本
DELETE /api/bookmarks/{panelId}         # 清除书签
```

### 同步组

```
GET    /api/sync-group                        # 获取当前同步组状态
POST   /api/sync-group/toggle/{panelId}       # 加入/移出同步组（附带 serviceId）
POST   /api/sync-group/start                  # 所有面板同时开始书签录制
POST   /api/sync-group/end                    # 所有面板同时结束书签录制
GET    /api/sync-group/export                 # 合并导出所有面板书签文本
```

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
}
```

### Project

```go
type Project struct {
    ID       string    `json:"id"`
    Name     string    `json:"name"`
    RootPath string    `json:"root_path"`
    Services []Service `json:"services"`
}
```

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

### LogBookmark（纯内存，不持久化）

每个面板最多一个活跃书签。开始时实时追加属于绑定服务的日志，结束后可导出。

```go
type LogBookmark struct {
    PanelID   string     `json:"panel_id"`
    ServiceID string     `json:"service_id"`
    StartTime *time.Time `json:"start_time,omitempty"`
    EndTime   *time.Time `json:"end_time,omitempty"`
    Logs      []LogEntry `json:"logs"`  // 内存中锁定的日志片段
}
```

### SyncGroupState（协调器状态，不是持久化实体）

同步组让多个面板同时开始/结束录制，时间对齐，方便对比同一时间段内不同服务的日志（如后端 A、后端 B 同时出问题时分栏对照）。

```go
type SyncGroupState struct {
    PanelServiceBindings map[string]string `json:"panel_service_bindings"` // panelId → serviceId
    IsRecording          bool              `json:"is_recording"`
}
```

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
| `LogFilter` 内存过滤 | agent 内存过滤 + WebSocket 推送 |
| `LogRule` 项目级规则 | 随项目配置持久化，API 读写 |
| `LogBookmark` 内存书签 | agent 内存书签，面板级管理 |
| `SyncGroup` 同步组 | agent `SyncGroupState` 协调器 |
| `ControlSocketServer` (UDS) | agent HTTP REST 接口 |
| `MenuBarManager` + SwiftUI | Tauri 系统托盘 + Vue 前端 |
| `UserDefaults` 持久化 | agent 本地 JSON 配置文件 |
| `superdev-mcp` CLI | 保留，改为连接 Go agent HTTP 接口 |
