# 日志系统深度优化设计

## 背景与目标

当前日志系统存在以下问题：
1. 启动后历史日志丢失，无法回溯上次运行记录
2. 过期删除逻辑存在但未被调用，DB 无限增长
3. UI 过滤过于简陋：单关键词、无排除、无持久化
4. 无法配置"背景噪音过滤"（如心跳包），每次都需手动过滤

目标：构建一个分层、可持久化的日志过滤体系，同时补全启动恢复和过期清理能力。

## 需求对齐

- 用户原始需求：日志存储性能、启动恢复、过期删除、多关键词+AND/OR+排除过滤
- 对齐后的理解：
  - 存储：单表够用，不拆表
  - 启动恢复：加载上一个 run + 历史 run 切换
  - 过期：按天删除，默认 7 天，可配置
  - 过滤：分两层——持久化项目级规则（存 config.yaml）+ 主界面临时快捷过滤

---

## 技术方案

### 架构总览

```
持久化规则（.superdev/config.yaml）
        ↓ 始终生效，后台过滤
    LogFilter（新增，纯函数规则引擎）
        ↓
实时 logs / historyLogs（AppCore）
        ↓ 叠加临时过滤
    LogPanelView（主界面快捷过滤）
```

### 关键模块

| 模块 | 变更 | 说明 |
|------|------|------|
| `LogRule` | 新增 Model | 单条过滤规则的数据结构 |
| `LogFilter` | 新增 | 纯函数规则引擎，执行规则匹配 |
| `LogStore` | 扩展 | 增加 deleteOldEntries、fetchRuns |
| `AppCore` | 扩展 | 启动恢复、historyLogs、availableRuns |
| `ConfigLoader` | 扩展 | 读写 logRules 配置段 |
| `LogPanelView` | 改造 | chip 式多关键词输入、规则入口按钮 |
| `LogRulesView` | 新增 | 持久化规则编辑 sheet |
| `LogHistoryView` | 新增 | 历史 run 选择与切换 |

---

## 数据层设计

### 1. 过期删除

`LogStore` 新增方法：

```swift
func deleteOldEntries(olderThan days: Int) {
    // DELETE FROM log_entries WHERE timestamp < now - days
}
```

- 在 `AppCore.init()` 中异步调用，默认 7 天
- 天数存 `UserDefaults`（key: `superdev.log_retention_days`），在 Settings 界面可调整

### 2. 启动恢复 / 历史记录

**RunSummary 结构**：

```swift
struct RunSummary: Identifiable {
    let runId: UUID
    let startTime: Date
    let logCount: Int
    let serviceNames: [String]
}
```

**AppCore 新增状态**：

```swift
@Published var availableRuns: [RunSummary] = []   // 历史 run 列表
@Published var historyLogs: [LogEntry] = []        // 当前查看的历史 run 日志
@Published var viewingRunId: UUID? = nil           // nil = 实时模式
```

**启动流程**：
1. `AppCore.init()` 异步调用 `LogStore.fetchRuns()` 填充 `availableRuns`
2. 自动加载上一个 run 的日志到 `historyLogs`（仅作为"上次记录"备用）
3. 用户切换历史 run 时，从 DB 异步加载到 `historyLogs`，UI 切换到历史模式
4. 历史模式顶部显示提示条，点击回到实时模式

**LogStore 新增**：

```swift
func fetchRuns() -> [RunSummary]
func fetchLogs(for runId: UUID) -> [LogEntry]
```

### 3. 存储结构

单张 `log_entries` 表保持不变，现有索引（`timestamp`、`service_id`、`run_id`、`level`）已满足所有查询需求，无需拆表。

---

## 过滤规则系统

### LogRule 模型

```swift
struct LogRule: Codable, Identifiable {
    let id: UUID
    var name: String           // 备注，如"心跳包"
    var type: RuleType         // include / exclude
    var keywords: [String]     // 关键词列表
    var logic: RuleLogic       // AND / OR
    var enabled: Bool

    enum RuleType: String, Codable { case include, exclude }
    enum RuleLogic: String, Codable { case and, or }
}
```

### config.yaml 新增字段

```yaml
logRules:
  retentionDays: 7
  rules:
    - id: "uuid-string"
      name: "心跳包"
      type: exclude
      keywords: ["heartbeat", "ping", "health check"]
      logic: OR
      enabled: true
```

### LogFilter 规则引擎

```swift
final class LogFilter {
    // 对一条日志应用规则集，返回是否应该显示
    static func passes(_ entry: LogEntry, rules: [LogRule]) -> Bool
    
    // 批量过滤（用于 historyLogs 加载后的初始过滤）
    static func apply(rules: [LogRule], to entries: [LogEntry]) -> [LogEntry]
}
```

**执行顺序**：
1. 先执行所有 `exclude` 规则（有任一 exclude 命中 → 不显示）
2. 再执行所有 `include` 规则（若有 include 规则且无一命中 → 不显示）
3. 无规则 = 全部通过

### 主界面临时过滤

临时过滤状态存在 `LogPanelView` 的 `@State` 中，不持久化：

```swift
struct FilterChip: Identifiable {
    let id: UUID
    var keyword: String
    var type: ChipType  // include / exclude
    enum ChipType { case include, exclude }
}

@State private var chips: [FilterChip] = []
@State private var chipLogic: ChipLogic = .or  // AND / OR
```

临时过滤在持久化规则之后叠加执行。

**「保存为规则」**：当 `chips` 非空时，toolbar 右侧出现保存按钮，点击弹出小 sheet，填 `name` 和选 `type`，确认后追加到 `config.yaml`。

---

## UI 设计

### 主界面 toolbar（改造）

```
[🔍 chip1 × ] [chip2 ×] [AND|OR] [+]    |  ERROR  WARN  INFO  |  [历史▾]  [⚙规则]
```

- 搜索区：输入关键词后回车/逗号生成 chip，chip 点击切换 `+include`/`-exclude` 状态
- chip 之间逻辑切换：AND / OR 小按钮
- `[历史▾]`：下拉列出历史 run
- `[⚙规则]`：进入持久化规则编辑 sheet

### 历史模式提示条

切换到历史 run 后，log list 顶部显示黄色提示条：

```
⏱ 查看历史记录：2026-05-18 10:23 · 342 条   [返回实时]
```

### 持久化规则编辑 sheet（LogRulesView）

macOS 系统偏好设置风格的列表：
- 每行：开关 | 名称 | 类型标签（包含/排除）| 关键词预览 | 编辑按钮
- 底部：「+ 添加规则」按钮
- 点击编辑弹出 rule editor：名称、类型、关键词（chip 输入）、AND/OR
- 改动实时写回 `config.yaml`，并通知 AppCore 重新加载规则

---

## 里程碑

### 里程碑 1：数据层
- `LogStore` 增加 `deleteOldEntries`、`fetchRuns`、`fetchLogs(for:)`
- `AppCore` 启动时调用过期删除
- `AppCore` 启动时加载上一个 run 日志 + `availableRuns`
- `RunSummary` 模型

### 里程碑 2：规则引擎
- `LogRule` 模型
- `LogFilter` 纯函数引擎
- `ConfigLoader` 读写 `logRules` 段
- `AppCore` 持有 `currentRules: [LogRule]`，过滤 `logs` 时应用

### 里程碑 3：主界面快捷过滤改造
- toolbar 改造为 chip 式多关键词输入
- AND/OR 切换
- 历史 run 下拉切换 + 历史模式提示条
- 「保存为规则」入口

### 里程碑 4：持久化规则编辑界面
- `LogRulesView` sheet
- 规则增删改、启用/禁用
- 实时写回 `config.yaml`

---

## 关键决策

### 1. 问题本质
日志系统缺乏"信噪比控制"能力——既没有持久化的噪音过滤，也没有临时的精确搜索，还丢失了历史上下文。这不是单点问题，是过滤体系缺失。

### 2. 为什么规则放 config.yaml
项目级规则（心跳包过滤等）是项目属性，不同项目噪音模式不同。放 config.yaml 可以 git 管理，团队共享。全局存储无法区分项目。

### 3. 为什么单表不拆表
当前数据量级（开发调试日志），SQLite 单表 + 索引完全够用。拆表只会让跨 run、跨服务查询变复杂，无收益。

### 4. 为什么 LogFilter 是纯函数
规则引擎无状态，便于单元测试，且可以在任意线程调用（历史日志加载后台过滤）。

### 5. 临时过滤 vs 持久化规则的关系
两层独立叠加：持久化规则是"始终生效的背景过滤"，临时过滤是"当前会话的精确搜索"。exclude 规则永远先于 include 规则执行，临时过滤最后叠加。

## 影响范围

**修改**：`LogStore.swift`、`AppCore.swift`、`ConfigLoader.swift`、`LogPanelView.swift`、`LogEntry.swift`（RunSummary）

**新增**：`LogRule.swift`、`LogFilter.swift`、`LogRulesView.swift`、`RunSummary.swift`（或并入 LogEntry）

**config.yaml schema**：新增 `logRules` 段，向后兼容（无此字段时用默认值）
