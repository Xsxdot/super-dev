# UI 改进设计文档

日期：2026-05-19

## 背景与目标

对 SuperDev macOS 应用的三个 UI 区域进行优化：
1. 日志面板过滤栏交互体验提升
2. 主窗口侧边栏精简
3. 设置页从单列改为侧边栏分类布局

---

## 改动 1 — 过滤栏：输入框前增加包含/排除分段控件

**位置**：`LogPanelView.swift` → `chipSearchArea`

**当前**：新标签默认为 include 类型，用户在标签添加后才能点击 +/− 切换类型。

**新方案**：在搜索图标（🔍）与输入框之间插入一个 Segmented Control，选项为「包含 / 排除」，控制下一个即将添加的标签的类型。

**行为细节**：
- Segmented Control 持有 `@State private var nextChipType: FilterChip.ChipType = .include`
- 用户回车添加标签时，使用 `nextChipType` 决定新标签类型（不再固定为 `.include`）
- 标签添加后，`nextChipType` 保持不变（用户可连续添加同类型）
- 标签上现有的 +/− 点击切换功能保留

---

## 改动 2 — 过滤栏：标签区分为临时标签 + 项目级规则快捷开关

**位置**：`LogPanelView.swift` → `chipSearchArea` 和 toolbar

**当前**：过滤栏只显示用户输入的临时标签，项目级规则需要点击 ⚙ 按钮打开独立 sheet 才能管理。

**新方案**：在临时标签区域右侧，用竖线分隔符（`Divider`）隔开，展示当前项目的所有持久规则作为可点击标签。

**项目级规则标签样式**：
- 启用状态：绿色背景（`Color.green.opacity(0.12)`），绿色文字，前缀显示规则类型（`↓排除` / `↑包含`）
- 禁用状态：灰色背景，文字加删除线，`opacity(0.5)`
- 点击：切换该规则的 `enabled` 状态并立即持久化（调用 `core.saveLogRules`）

**数据来源**：`core.logRules(for: activeProject)` 读取项目规则列表，若 `activeProject == nil` 则不显示此区域。

---

## 改动 3 — 过滤栏：临时标签增加「保存到项目规则」按钮

**位置**：`LogPanelView.swift` → `chipView(_:)`

**当前**：临时标签只有 +/− 类型切换和 ✕ 删除按钮。

**新方案**：在 ✕ 左侧增加一个 ⬆ 图标按钮（`square.and.arrow.up` SF Symbol），点击后将该关键词以当前类型快速添加到项目级规则。

**行为细节**：
- 若 `activeProject == nil`，⬆ 按钮禁用（`.disabled(true)`）
- 点击后：创建一个同名（关键词作为规则名）、同类型、单关键词的 `LogRule`，append 到项目 config，调用 `core.saveLogRules`
- 不删除临时标签，用户可继续使用临时过滤
- 不显示保存成功 toast（避免打扰），规则立即出现在右侧项目级规则区域即为反馈

---

## 改动 4 — 移除侧边栏的 All Logs

**位置**：`SidebarView.swift`

**当前**：每个项目 Section 下第一项是 `Label("All Logs", ...)` 绑定到 `Optional<UUID>.none`。

**新方案**：删除该行。

**默认选中处理**：`MainWindowView` 中 `selectedServiceId` 初始为 `nil`，需改为在 `onAppear` 时自动选中第一个可用服务（`core.projects.first?.services.first?.id`）。`LogPanelView` 中 `serviceId: nil` 时显示所有服务日志的逻辑维持不变（作为内部兜底，不再作为用户入口暴露）。

---

## 改动 5 — 设置页改为侧边栏分类布局

**位置**：`SettingsView.swift`

**当前**：单列垂直堆叠，所有内容放在同一个 `VStack`。

**新方案**：采用 `NavigationSplitView`（或 `HSplitView` + 手动 List），分为 3 个分类：

| 分类 | 图标 | 内容 |
|------|------|------|
| 通用 | `gearshape` | 日志保留天数（现有 `logRetentionSection`） |
| 项目 | `folder` | 项目列表 + 服务可见性 + 添加项目按钮（现有 `projectList` + `addButton`） |
| 集成 | `puzzlepiece` | MCP 信息 + 复制配置按钮（现有 `mcpSection`） |

**布局**：
- 整体尺寸改为 `width: 600, height: 420`（比当前 `width: 480` 更宽以容纳侧边栏）
- 侧边栏固定宽度 `140pt`，使用 `List` + `selection` 绑定当前分类
- 内容区使用 `@ViewBuilder` switch 分发到对应子视图

**默认选中**：打开设置时默认选中「通用」。

---

## 影响范围

| 文件 | 改动类型 |
|------|---------|
| `SuperDev/UI/MainWindow/LogPanelView.swift` | 修改（改动 1、2、3） |
| `SuperDev/UI/MainWindow/SidebarView.swift` | 修改（改动 4） |
| `SuperDev/UI/MainWindow/MainWindowView.swift` | 修改（改动 4 默认选中） |
| `SuperDev/UI/Settings/SettingsView.swift` | 修改（改动 5） |

无新增文件，无新依赖。

---

## 不在范围内

- 日志过滤逻辑本身（`core.filteredLogs`）不变
- `LogRulesView`（规则编辑 sheet）不变，仍通过 ⚙ 按钮访问
- `AddProjectView` 不变
