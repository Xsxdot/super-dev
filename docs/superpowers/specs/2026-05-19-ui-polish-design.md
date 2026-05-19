# SuperDev UI 优化设计文档

**日期**：2026-05-19  
**范围**：6 项 UI 体验和视觉一致性优化

---

## 背景与目标

SuperDev 是一个 macOS 菜单栏开发服务管理器。当前 UI 存在以下问题：
1. 日志面板自动滚底打断用户查看历史日志
2. `LogPanelView` 使用硬编码颜色，与 Theme 体系不一致
3. 状态栏错误/警告计数显示粗糙，缺乏视觉区分
4. `SettingsView` 使用系统默认白色控件，与整体深色主题脱节
5. 日志行缺少快速复制能力
6. 服务退出后无错误上下文提示

---

## 优化项详细设计

### 1. 日志自动跟随 — 浮动徽章（LogPanelView）

**行为规则**：
- 默认启用自动跟随（始终滚到底部）
- 用户向上滚动时，停止自动跟随
- 右下角出现蓝色浮层「↓ N 条新日志」，N 为用户未看到的新增条数
- 点击浮层 → 跳到底部 + 恢复自动跟随
- 用户手动滚回底部时，浮层自动消失、自动跟随恢复

**实现**：
- 新增 `@State private var isFollowing: Bool = true`
- 新增 `@State private var newLogCount: Int = 0`
- 使用 `ScrollView` 的 `onScrollGeometryChange` 检测是否在底部附近（距底 < 50pt）来控制 `isFollowing`
- `isFollowing == false` 时，`onChange(of: filteredLogs.count)` 累加 `newLogCount` 而不是滚动
- `isFollowing == true` 时保持原有自动滚底逻辑，`newLogCount` 重置为 0
- 浮层用 `.overlay(alignment: .bottomTrailing)` 叠在 `logList` 上，仅在 `!isFollowing && newLogCount > 0` 时显示

**修改文件**：`SuperDev/UI/MainWindow/LogPanelView.swift`

---

### 2. 颜色统一到 Theme（LogPanelView）

将 `LogPanelView` 中 3 处硬编码颜色替换为 Theme 常量：

| 位置 | 旧值 | 新值 |
|------|------|------|
| `logList` 背景 | `Color(red: 0.12, green: 0.12, blue: 0.12)` | `Theme.bgSecondary` |
| 时间戳颜色 | `Color(white: 0.45)` | `Theme.textTertiary` |
| 日志正文颜色 | `Color(white: 0.85)` | `Theme.textPrimary` |

不新增 Theme 常量，不改其他文件。

**修改文件**：`SuperDev/UI/MainWindow/LogPanelView.swift`

---

### 3. 状态栏 badge 美化（LogPanelView）

**改后样式**：`statusBar` 中的错误/警告计数改为带背景色的 badge，与 PopoverView 的 `statusBadge` 风格一致：
- 错误数：红色系背景 + 红色文字，格式「● N 错误」
- 警告数：黄色系背景 + 黄色文字，格式「● N 警告」
- 边框：对应颜色 20% 透明度描边

在 `statusBar` 内直接内联实现 badge 样式，不跨文件抽取共用组件。

**修改文件**：`SuperDev/UI/MainWindow/LogPanelView.swift`

---

### 4. SettingsView 全深色重写

完全用 Theme 颜色重写 `SettingsView`，布局结构保持不变（列表 + 底部按钮）。

**样式规范**：
- 窗口背景：`Theme.bgPrimary`
- 列表行背景：`Theme.bgElevated`，分隔线：`Theme.borderPrimary`
- 项目名：`Theme.textPrimary` + `.fontWeight(.medium)`
- 路径：`Theme.textSecondary`，`.font(.caption)`，截断中间
- 「重新加载」按钮图标：`Theme.textSecondary`
- 「删除」按钮图标：`Theme.statusFailed`（红色）
- 「添加项目」按钮：`Theme.accent` 背景，白色文字，`.cornerRadius(6)`，替换 `.borderedProminent`

不修改 `AddProjectView`。

**修改文件**：`SuperDev/UI/Settings/SettingsView.swift`

---

### 5. 日志行右键复制（LogPanelView）

在 `logRow` 方法上添加 `.contextMenu`，提供两个菜单项：

- **「复制此行」**：格式为 `{HH:mm:ss} [{serviceName}] {LEVEL} {message}`，写入系统剪贴板
- **「复制消息」**：仅 `entry.message`，写入系统剪贴板

使用 `NSPasteboard.general.clearContents()` + `setString(_:forType: .string)` 写入。保留现有 `textSelection(.enabled)` 不变。

**修改文件**：`SuperDev/UI/MainWindow/LogPanelView.swift`

---

### 6. 服务退出 Tooltip 错误提示（LogStore + PopoverView）

**数据层**：在 `LogStore` 新增查询方法：
```swift
func lastErrorLog(for serviceId: UUID) -> LogEntry?
```
返回该服务最后一条 `.error` 或 `.unknown` 级别日志。不修改现有接口。

**UI 层**：`PopoverView` 的 `serviceRow` 中，「已退出」状态文字加 `.help(tooltipText)` tooltip：
- 当 `service.status == .failed` 且 `lastErrorLog` 有值时，tooltip 显示该条 `message`（截断到 120 字符）
- 其他情况 tooltip 为空字符串（不展示）

**修改文件**：
- `SuperDev/AppCore/Log/LogStore.swift`
- `SuperDev/UI/MenuBar/PopoverView.swift`

---

## 修改文件汇总

| 文件 | 涉及改动 |
|------|---------|
| `SuperDev/UI/MainWindow/LogPanelView.swift` | 优化项 1、2、3、5 |
| `SuperDev/UI/Settings/SettingsView.swift` | 优化项 4 |
| `SuperDev/AppCore/Log/LogStore.swift` | 优化项 6（新增方法） |
| `SuperDev/UI/MenuBar/PopoverView.swift` | 优化项 6（UI tooltip） |

---

## 不在范围内

- `AddProjectView` 样式（独立改动，影响面另算）
- `SidebarView` 的 "All Logs" tag 逻辑问题（问题1，本次不做）
- `PopoverView` hover 触发逻辑（问题2，本次不做）
