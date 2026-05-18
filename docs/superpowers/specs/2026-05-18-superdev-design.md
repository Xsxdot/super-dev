# SuperDev — 设计文档

**版本**：v1.0（第一阶段）
**日期**：2026-05-18

---

## 背景与目标

开发者日常需要同时管理多个项目、每个项目有多个子进程（后端服务、前端、桌面端等），目前只能靠打开 IDE 或手动跑命令来启动，切换成本高、日志分散。

SuperDev 是一个 macOS 菜单栏常驻工具，目标是：
- 一键启停任意项目的任意子进程组合
- 聚合所有子进程日志，过滤噪音，快速定位问题
- 为后续 AI 智能体集成（MCP Server、CLI）打好架构基础

---

## 产品范围

### 第一阶段（本文档范围）
- macOS 菜单栏 App（SwiftUI 原生）
- Popover 两层交互 + 主窗口日志面板
- 进程管理：启停、分组、状态监控
- 日志：聚合、过滤、搜索、去重折叠、持久化
- 配置：`.superdev/config.yaml` + GUI 添加项目 + 导入 `launch.json`

### 第二阶段（后续迭代，不在本文档）
- Debug 集成：自动注入 debugger 参数（dlv / --inspect），自动生成 `launch.json`
- Core Daemon：进程管理抽离为后台服务
- CLI：命令行控制接口
- MCP Server：AI 智能体（Claude Code、Codex）交互层

---

## 架构

### 第一阶段架构

```
┌─────────────────────────────────────────┐
│           SwiftUI App                   │
│  ┌─────────────┐  ┌──────────────────┐  │
│  │  MenuBar +  │  │   主窗口          │  │
│  │  Popover    │  │   日志面板        │  │
│  └──────┬──────┘  └────────┬─────────┘  │
│         └────────┬─────────┘            │
│            ┌─────▼──────┐               │
│            │  AppCore   │               │
│            │ (进程管理   │               │
│            │  + 日志)   │               │
│            └─────┬──────┘               │
└──────────────────┼──────────────────────┘
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
   Process      SQLite     .superdev/
  (Foundation)  (日志存储)  config.yaml
```

### 第二阶段目标架构

```
CLI / MCP Server          ← AI 智能体交互层
        ↕
  Core Daemon             ← 后台进程管理引擎
        ↕
  SwiftUI App             ← 人机交互层
```

第一阶段的 AppCore 模块设计时需为 Daemon 化预留接口边界。

---

## 交互设计

### 菜单栏图标
状态通过图标颜色反映：
- 灰色：所有项目未启动
- 绿色：有项目运行中，无错误
- 黄色：有进程启动中
- 红色：有进程异常/退出

### Popover（两层）

**一级：项目列表**
```
┌─────────────────────┐
│ 项目                 │
│ ● blog-system      › │
│ ● MyProject        › │  ← hover 展开二级
│ ○ side-tool        › │
│─────────────────────│
│ + 添加项目           │
└─────────────────────┘
```
- 状态指示灯：绿色=运行中，黄色=部分运行，灰色=未启动，红色=有异常

**二级：子进程面板**（hover 项目后右侧展开）
```
┌─────────────────────────────────────────────┐
│ MyProject              [全部停止] [▶ 启动选中] │
│ ☑ 全选                              [反选]   │
│─────────────────────────────────────────────│
│ 必须启动                                      │
│ ☑ ● gateway       运行中              [■]   │
│ ☑ ● user-service  运行中              [■]   │
│─────────────────────────────────────────────│
│ 可选                                         │
│ ☐ ○ admin-web     未启动              [▶]   │
│ ☐ ◑ desktop-app   启动中…             [■]   │
│─────────────────────────────────────────────│
│                              [📋 查看日志 →] │
└─────────────────────────────────────────────┘
```

### 主窗口

**布局**：左侧进程树 + 右侧日志面板

```
┌──────────────┬──────────────────────────────────────────┐
│ MyProject    │ [🔍 关键词]  [ERROR] [WARN] [INFO]        │
│  All Logs    │──────────────────────────────────────────│
│  gateway     │ 10:23:01 [gateway]    INFO  Started :8080 │
│  user-svc    │ 10:23:02 [user-svc]   INFO  DB connected  │
│  admin-web   │ 10:23:03 [gateway]    WARN  timeout ×47   │
│  desktop-app │ 10:23:07 [gateway]    ERROR conn refused  │
│              │──────────────────────────────────────────│
│              │ 共 1,243 条 · 折叠 47 条    1E · 1W      │
└──────────────┴──────────────────────────────────────────┘
```

**日志功能细节**：
- 多进程日志按来源着色区分（每个进程固定一个颜色）
- Level 过滤：ERROR / WARN / INFO，可组合
- 关键词搜索：实时过滤
- 重复折叠：相同内容（忽略时间戳）连续出现，折叠为一条 + ×N 徽章
- 错误行高亮：ERROR 行红色背景，WARN 行黄色背景
- 历史持久化：每次运行记录保存，可切换查看历次运行日志

---

## 配置格式

### 主配置文件

路径：`{项目根目录}/.superdev/config.yaml`

```yaml
name: MyProject
description: 主业务项目

services:
  - name: gateway
    command: go run ./cmd/gateway
    working_dir: .
    required: true
    env_file: .env
    env:
      PORT: "8080"
      LOG_LEVEL: debug

  - name: user-service
    command: go run ./cmd/user
    working_dir: .
    required: true
    env_file: .env.local

  - name: admin-web
    command: pnpm dev
    working_dir: ./admin
    required: false

  - name: desktop-app
    command: cargo tauri dev
    working_dir: ./desktop
    required: false
```

**字段说明**：
- `required`：true = 必须启动分组；false = 可选分组
- `env_file`：引用 `.env` 文件，相对于项目根目录
- `env`：直接在配置中定义环境变量，与 `env_file` 合并，直接定义的优先级更高
- `working_dir`：进程的工作目录，相对于项目根目录

### launch.json 导入

支持将 `.vscode/launch.json` 作为导入源，转换规则：
- `configurations[].name` → `service.name`
- `configurations[].program` + `configurations[].args` → `service.command`
- `configurations[].cwd` → `service.working_dir`
- `configurations[].env` → `service.env`

导入后生成 `.superdev/config.yaml`，后续以 yaml 为主格式。

---

## 数据模型

### 核心实体

```swift
struct Project {
    let id: UUID
    let name: String
    let rootPath: String        // 项目根目录绝对路径
    var services: [Service]
}

struct Service {
    let id: UUID
    let name: String
    let command: String
    let workingDir: String
    let required: Bool
    let envFile: String?
    let env: [String: String]
    var status: ServiceStatus
}

enum ServiceStatus {
    case stopped
    case starting
    case running(pid: Int32)
    case failed(exitCode: Int32)
}
```

### 日志条目

```swift
struct LogEntry {
    let id: UUID
    let timestamp: Date
    let serviceId: UUID
    let level: LogLevel         // error / warn / info / debug / unknown
    let message: String
    let runId: UUID             // 标识本次运行，用于历史切换
    var repeatCount: Int        // 重复次数，默认 1
}

enum LogLevel {
    case error, warn, info, debug, unknown
}
```

---

## 技术选型

| 模块 | 技术 | 说明 |
|------|------|------|
| UI | SwiftUI | macOS 原生，最小 macOS 14.0 |
| 菜单栏 | NSStatusItem + NSPopover | Foundation |
| 进程管理 | Foundation.Process | 标准库，stdout/stderr pipe |
| 日志存储 | SQLite（GRDB.swift） | 本地持久化，查询灵活 |
| 配置解析 | Yams | Swift YAML 解析库 |
| 项目管理 | Swift Package Manager | 无 Xcode workspace 依赖 |

---

## 日志去重算法

同一 `serviceId`，相邻两条日志满足以下条件时触发折叠：
1. `level` 相同
2. `message` 相同（去除时间戳、行号等可变部分后）

折叠时：更新最新时间戳，`repeatCount += 1`，不新增条目。

消息规范化规则（去除可变部分）：
- 去除开头的时间戳（`\d{2}:\d{2}:\d{2}` 等常见格式）
- 去除数字 ID（如 `uid=1042` → `uid=*`）
- 去除 IP + 端口（`127.0.0.1:5432` → `*:*`）

---

## 关键决策

### 为什么用 SQLite 而不是文件
日志量大时文件追加容易达到读取性能瓶颈；SQLite 支持按 level、时间、serviceId 快速过滤，历史运行切换也更自然（按 runId 查询）。

### 为什么配置文件放 `.superdev/` 而不是项目根
- 避免污染项目根目录
- 可以整个目录加入 `.gitignore`（如果不想提交），也可以提交共享给团队
- 后续 Daemon socket、运行时状态等文件都放这里

### 为什么 launch.json 只做导入源
`launch.json` 是 debugger 配置，字段语义与进程管理不一致；强行复用会造成概念混乱。导入转换是最小摩擦的接入方式，后续第二阶段反向生成 `launch.json` 时也更灵活。

### 为什么第一阶段不做 Daemon
降低复杂度，App 即引擎；但 AppCore 模块要保持清晰的接口边界，为第二阶段 Daemon 化预留。

---

## 不在范围

- AI 日志分析（第二阶段独立子项目）
- Debug 自动注入（第二阶段）
- CLI / MCP Server（第二阶段）
- Windows / Linux 支持（不在计划内）
- 远程进程管理（不在计划内）
