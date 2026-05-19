# UI Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 优化三个 UI 区域：日志过滤栏交互、侧边栏移除 All Logs、设置页改为侧边栏分类布局。

**Architecture:** 纯 SwiftUI 视图层改动，不涉及数据模型或 AppCore 业务逻辑变更。`LogPanelView` 增加输入类型预选、项目规则快捷标签、标签内保存按钮；`SidebarView` 删除 All Logs 行；`SettingsView` 改为 `NavigationSplitView` 三分类布局。

**Tech Stack:** SwiftUI, macOS, AppCore (已有 `addLogRule`、`saveLogRules`、`logRules(for:)` API)

---

## 文件修改清单

| 文件 | 操作 |
|------|------|
| `SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift` | 修改（改动 1、2、3） |
| `SuperDev/SuperDev/UI/MainWindow/SidebarView.swift` | 修改（改动 4） |
| `SuperDev/SuperDev/UI/MainWindow/MainWindowView.swift` | 修改（改动 4 默认选中） |
| `SuperDev/SuperDev/UI/Settings/SettingsView.swift` | 修改（改动 5） |

---

## Task 1: 过滤栏 — 输入框前增加包含/排除分段控件

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift`

- [ ] **Step 1: 在 LogPanelView 的 State 区增加 nextChipType**

在 `@State private var chipInput` 附近（约第 45 行）增加新的 State：

```swift
@State private var nextChipType: FilterChip.ChipType = .include
```

- [ ] **Step 2: 修改 chipSearchArea 插入 Segmented Control**

将 `chipSearchArea` 计算属性（约第 138 行）中，`Image(systemName: "magnifyingglass")` 之后、`ForEach(chips)` 之前插入 Segmented Control：

```swift
private var chipSearchArea: some View {
    HStack(spacing: 6) {
        Image(systemName: "magnifyingglass")
            .foregroundColor(.secondary)
            .font(.system(size: 11))

        Picker("", selection: $nextChipType) {
            Text("包含").tag(FilterChip.ChipType.include)
            Text("排除").tag(FilterChip.ChipType.exclude)
        }
        .pickerStyle(.segmented)
        .frame(width: 88)
        .labelsHidden()

        ForEach(chips) { chip in
            chipView(chip)
        }

        if !chips.isEmpty {
            Button(chipLogic.label) { chipLogic.toggle() }
                .font(.system(size: 10, weight: .semibold))
                .padding(.horizontal, 5)
                .padding(.vertical, 2)
                .background(Color.secondary.opacity(0.15))
                .cornerRadius(4)
                .buttonStyle(.plain)
                .help("切换关键词之间的 AND / OR 逻辑")
        }

        TextField(chips.isEmpty ? "关键词过滤，回车添加" : "添加关键词…", text: $chipInput)
            .textFieldStyle(.plain)
            .frame(minWidth: 80, maxWidth: 140)
            .onSubmit { addChipFromInput() }
            .onChange(of: chipInput) { _, newValue in
                if newValue.contains(",") {
                    let parts = newValue.split(separator: ",")
                    for part in parts.dropLast() {
                        addChip(String(part))
                    }
                    chipInput = String(parts.last ?? "")
                }
            }

        if chips.isEmpty {
            Button {
                addChipFromInput()
            } label: {
                Image(systemName: "plus.circle")
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .help("添加关键词")
        }
    }
}
```

- [ ] **Step 3: 修改 addChip 使用 nextChipType**

将 `addChip(_:)` 方法（约第 514 行）改为接收类型参数，并修改调用处：

```swift
private func addChipFromInput() {
    addChip(chipInput, type: nextChipType)
    chipInput = ""
}

private func addChip(_ text: String, type: FilterChip.ChipType = .include) {
    let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return }
    guard !chips.contains(where: { $0.keyword.caseInsensitiveCompare(trimmed) == .orderedSame }) else { return }
    chips.append(FilterChip(keyword: trimmed, type: type))
}
```

注意：`onChange(of: chipInput)` 中通过逗号批量添加的路径调用 `addChip(String(part))`，这里不传 type 参数，默认 `.include`，保持原有行为即可。若希望批量添加也遵循 nextChipType，改为 `addChip(String(part), type: nextChipType)`。

- [ ] **Step 4: 编译验证**

在 Xcode 中 Build（⌘B），确认无编译错误。

- [ ] **Step 5: 手动测试**

启动 App，打开任意服务日志面板：
1. 过滤栏左侧出现「包含 / 排除」Segmented Control
2. 选「排除」后输入关键词回车，生成的标签显示为橙色（排除类型）
3. 切换为「包含」后再添加，生成蓝色标签

- [ ] **Step 6: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift
git commit -m "feat: add include/exclude segmented control to filter bar input"
```

---

## Task 2: 过滤栏 — 临时标签增加「保存到项目规则」按钮

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift`

- [ ] **Step 1: 修改 chipView 增加保存按钮**

将 `chipView(_:)` 方法（约第 186 行）完整替换：

```swift
private func chipView(_ chip: FilterChip) -> some View {
    HStack(spacing: 3) {
        Button {
            toggleChipType(chip.id)
        } label: {
            Text(chip.type == .include ? "+" : "−")
                .font(.system(size: 9, weight: .bold))
                .foregroundColor(chip.type == .include ? .blue : .orange)
        }
        .buttonStyle(.plain)

        Text(chip.keyword)
            .font(.system(size: 11))
            .lineLimit(1)

        // 竖线分隔
        Rectangle()
            .fill(Color.secondary.opacity(0.3))
            .frame(width: 1, height: 10)

        Button {
            saveChipToProjectRule(chip)
        } label: {
            Image(systemName: "square.and.arrow.up")
                .font(.system(size: 8))
                .foregroundColor(.green)
        }
        .buttonStyle(.plain)
        .help("保存到项目规则")
        .disabled(activeProject == nil)

        Button {
            chips.removeAll { $0.id == chip.id }
        } label: {
            Image(systemName: "xmark")
                .font(.system(size: 8))
                .foregroundColor(.secondary)
        }
        .buttonStyle(.plain)
    }
    .padding(.horizontal, 6)
    .padding(.vertical, 3)
    .background(chip.type == .include ? Color.blue.opacity(0.12) : Color.orange.opacity(0.12))
    .cornerRadius(4)
}
```

- [ ] **Step 2: 添加 saveChipToProjectRule 方法**

在 `// MARK: - Helpers` 区域（约第 509 行）末尾添加：

```swift
private func saveChipToProjectRule(_ chip: FilterChip) {
    guard let proj = activeProject else { return }
    let ruleType: LogRule.RuleType = chip.type == .include ? .include : .exclude
    let rule = LogRule(
        name: chip.keyword,
        type: ruleType,
        keywords: [chip.keyword],
        logic: .or,
        enabled: true
    )
    try? core.addLogRule(rule, to: proj)
}
```

- [ ] **Step 3: 编译验证**

⌘B，确认无编译错误。

- [ ] **Step 4: 手动测试**

1. 添加一个临时标签
2. 标签内出现绿色 ⬆ 图标
3. 点击 ⬆，打开 ⚙ 规则 sheet，确认新规则出现在列表中
4. 若无项目关联（serviceId == nil 且无法 resolve project），⬆ 按钮为禁用状态

- [ ] **Step 5: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift
git commit -m "feat: add save-to-project-rule button on filter chips"
```

---

## Task 3: 过滤栏 — 标签区右侧显示项目级规则快捷开关

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift`

- [ ] **Step 1: 在 chipSearchArea 末尾加分隔符和项目规则标签**

在 `chipSearchArea` 的 `HStack` 末尾（`if chips.isEmpty { ... }` 之后），添加项目规则区域：

```swift
// 项目级规则快捷开关（仅在有项目时显示）
if let proj = activeProject {
    let rules = core.logRules(for: proj.id)
    if !rules.isEmpty {
        Divider().frame(height: 14)
        ForEach(rules) { rule in
            projectRuleChip(rule, project: proj)
        }
    }
}
```

注意：`core.logRules(for: proj.id)` 返回 `[LogRule]`（使用 `@Published var logRulesByProjectId`），会随持久化自动刷新。

- [ ] **Step 2: 添加 projectRuleChip 方法**

在 `chipView(_:)` 方法之后添加：

```swift
private func projectRuleChip(_ rule: LogRule, project: Project) -> some View {
    Button {
        toggleProjectRule(rule, project: project)
    } label: {
        HStack(spacing: 3) {
            Text(rule.type == .include ? "↑" : "↓")
                .font(.system(size: 9, weight: .bold))
                .foregroundColor(rule.enabled ? (rule.type == .include ? .blue : .green) : .secondary)
            Text(rule.name.isEmpty ? rule.keywords.first ?? "" : rule.name)
                .font(.system(size: 11))
                .lineLimit(1)
                .strikethrough(!rule.enabled, color: .secondary)
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 3)
        .background(rule.enabled
            ? (rule.type == .include ? Color.blue.opacity(0.10) : Color.green.opacity(0.10))
            : Color.secondary.opacity(0.08))
        .cornerRadius(4)
        .opacity(rule.enabled ? 1.0 : 0.55)
    }
    .buttonStyle(.plain)
    .help(rule.enabled ? "点击禁用此规则" : "点击启用此规则")
}
```

- [ ] **Step 3: 添加 toggleProjectRule 方法**

在 `saveChipToProjectRule` 之后添加：

```swift
private func toggleProjectRule(_ rule: LogRule, project: Project) {
    var config = core.logRules(for: project)
    guard let idx = config.rules.firstIndex(where: { $0.id == rule.id }) else { return }
    config.rules[idx].enabled.toggle()
    try? core.saveLogRules(config, for: project)
}
```

- [ ] **Step 4: 编译验证**

⌘B，确认无编译错误。

- [ ] **Step 5: 手动测试**

1. 先通过 ⚙ 规则 sheet 为当前项目添加 1-2 条规则
2. 关闭 sheet，过滤栏右侧（分隔符后）出现规则标签
3. 点击规则标签，颜色变灰+删除线，日志列表随即刷新
4. 再次点击恢复启用

- [ ] **Step 6: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift
git commit -m "feat: show project rules as quick-toggle chips in filter bar"
```

---

## Task 4: 侧边栏移除 All Logs + 默认选中首个服务

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/SidebarView.swift`
- Modify: `SuperDev/SuperDev/UI/MainWindow/MainWindowView.swift`

- [ ] **Step 1: 删除 SidebarView 中的 All Logs 行**

打开 `SidebarView.swift`，将 Section 内容从：

```swift
Section(project.name) {
    Label("All Logs", systemImage: "doc.text.magnifyingglass")
        .tag(Optional<UUID>.none)

    ForEach(project.services) { service in
        // ...
    }
}
```

改为：

```swift
Section(project.name) {
    ForEach(project.services) { service in
        HStack {
            Circle()
                .fill(serviceStatusColor(service.status))
                .frame(width: 7, height: 7)
            Text(service.name)
        }
        .tag(Optional(service.id))
    }
}
```

同时删除注释 `// nil = All Logs`（第 6 行）。

- [ ] **Step 2: MainWindowView 增加 onAppear 默认选中首个服务**

打开 `MainWindowView.swift`，在 `NavigationSplitView` 后添加 `.onAppear`：

```swift
var body: some View {
    NavigationSplitView {
        SidebarView(
            selectedProjectId: $selectedProjectId,
            selectedServiceId: $selectedServiceId
        )
    } detail: {
        LogPanelView(
            serviceId: selectedServiceId,
            project: selectedProject
        )
    }
    .navigationTitle("SuperDev")
    .frame(minWidth: 800, minHeight: 500)
    .onAppear {
        if selectedServiceId == nil {
            selectedServiceId = core.projects.first?.services.first?.id
        }
    }
}
```

- [ ] **Step 3: 编译验证**

⌘B，确认无编译错误。

- [ ] **Step 4: 手动测试**

1. 启动 App，侧边栏不再显示 All Logs 入口
2. 首次打开主窗口，自动选中第一个服务（高亮显示）
3. 若项目为空（无服务），`selectedServiceId` 保持 nil，日志面板正常显示（兜底逻辑不变）

- [ ] **Step 5: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/SidebarView.swift \
        SuperDev/SuperDev/UI/MainWindow/MainWindowView.swift
git commit -m "feat: remove All Logs from sidebar, auto-select first service"
```

---

## Task 5: 设置页改为侧边栏分类布局

**Files:**
- Modify: `SuperDev/SuperDev/UI/Settings/SettingsView.swift`

- [ ] **Step 1: 增加分类枚举和选中状态**

在 `SettingsView` struct 内，`@State private var showAddProject` 之前插入：

```swift
private enum SettingsTab: String, CaseIterable, Identifiable {
    case general = "通用"
    case projects = "项目"
    case integrations = "集成"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .general: return "gearshape"
        case .projects: return "folder"
        case .integrations: return "puzzlepiece"
        }
    }
}

@State private var selectedTab: SettingsTab = .general
```

- [ ] **Step 2: 重写 body 为 NavigationSplitView**

将整个 `body` 替换为：

```swift
var body: some View {
    NavigationSplitView {
        List(SettingsTab.allCases, selection: $selectedTab) { tab in
            Label(tab.rawValue, systemImage: tab.icon)
                .tag(tab)
        }
        .listStyle(.sidebar)
        .navigationSplitViewColumnWidth(min: 140, ideal: 150, max: 160)
    } detail: {
        Group {
            switch selectedTab {
            case .general:
                generalPane
            case .projects:
                projectsPane
            case .integrations:
                integrationsPane
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
    .onAppear {
        retentionDays = core.logRetentionDays
    }
    .frame(width: 600, height: 420)
    .sheet(isPresented: $showAddProject) {
        AddProjectView().environmentObject(core)
    }
}
```

- [ ] **Step 3: 添加 generalPane**

在 `body` 之后，将原 `logRetentionSection` 改名为 `generalPane`：

```swift
private var generalPane: some View {
    VStack(alignment: .leading, spacing: 0) {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("日志保留天数")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(Theme.textPrimary)
                Text("超过此天数的日志将在启动时自动删除")
                    .font(.caption)
                    .foregroundColor(Theme.textSecondary)
            }
            Spacer()
            Stepper("\(retentionDays) 天", value: $retentionDays, in: 1...90)
                .onChange(of: retentionDays) { _, newValue in
                    core.logRetentionDays = newValue
                }
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 16)
        .background(Theme.bgElevated)
        .cornerRadius(8)
        .padding(16)
        Spacer()
    }
    .background(Theme.bgPrimary)
}
```

- [ ] **Step 4: 添加 projectsPane**

将原 `projectList` + `addButton` 合并为 `projectsPane`：

```swift
private var projectsPane: some View {
    VStack(spacing: 0) {
        HStack {
            Text("项目")
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Theme.textPrimary)
            Spacer()
            Button {
                showAddProject = true
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "plus")
                    Text("添加项目")
                }
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(.white)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(Theme.accent)
                .cornerRadius(6)
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)

        ScrollView {
            VStack(spacing: 1) {
                ForEach(core.projects) { project in
                    projectRow(project)
                }
            }
            .padding(.horizontal, 8)
            .padding(.bottom, 8)
        }
        Spacer(minLength: 0)
    }
    .background(Theme.bgPrimary)
}
```

- [ ] **Step 5: 添加 integrationsPane**

将原 `mcpSection` 改名为 `integrationsPane`：

```swift
private var integrationsPane: some View {
    VStack(alignment: .leading, spacing: 10) {
        Text("MCP 集成")
            .font(.system(size: 12, weight: .semibold))
            .foregroundColor(Theme.textPrimary)

        VStack(alignment: .leading, spacing: 4) {
            Text("Control socket")
                .font(.caption)
                .foregroundColor(Theme.textSecondary)
            Text(ControlSocketServer.socketPath)
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(Theme.textTertiary)
                .lineLimit(1)
                .truncationMode(.middle)
        }

        Button {
            copyMCPConfig()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "doc.on.clipboard")
                Text("复制 Claude Code 配置")
            }
            .font(.system(size: 11, weight: .medium))
            .foregroundColor(Theme.accent)
        }
        .buttonStyle(.plain)
        .help("复制后粘贴到 .claude/settings.json 的 mcpServers 字段")

        Spacer()
    }
    .padding(20)
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    .background(Theme.bgPrimary)
}
```

- [ ] **Step 6: 删除已废弃的原方法**

删除 `SettingsView` 中不再使用的以下私有方法/属性（已被新 pane 方法取代）：
- `logRetentionSection`
- `projectList`（已合并入 `projectsPane`）
- `addButton`（已合并入 `projectsPane`）
- `mcpSection`（已改名为 `integrationsPane`）

保留 `projectRow(_:)`、`serviceVisibilityRow(_:)`、`copyMCPConfig()` 不变。

- [ ] **Step 7: 编译验证**

⌘B，确认无编译错误。重点检查是否有残留的旧方法引用。

- [ ] **Step 8: 手动测试**

1. 打开 Settings（菜单栏 → SuperDev → Preferences 或 ⌘,）
2. 窗口尺寸为 600×420，左侧有三个分类
3. 「通用」：显示日志保留天数 Stepper，可调节，关闭重开设置确认保持
4. 「项目」：显示项目列表 + 右上角添加项目按钮，可正常添加/删除项目
5. 「集成」：显示 MCP socket 路径和复制按钮，点击复制后粘贴到文本编辑器确认内容正确

- [ ] **Step 9: Commit**

```bash
git add SuperDev/SuperDev/UI/Settings/SettingsView.swift
git commit -m "feat: refactor settings to sidebar layout with General/Projects/Integrations tabs"
```

---

## 完成检查

所有 Task 完成后：
- [ ] 过滤栏：Segmented Control 包含/排除切换正常
- [ ] 过滤栏：临时标签有 ⬆ 保存按钮，点击后规则出现在右侧
- [ ] 过滤栏：项目级规则在分隔符右侧展示，点击可切换启用/禁用
- [ ] 侧边栏：无 All Logs 入口，启动自动选中首个服务
- [ ] 设置页：三分类侧边栏布局，内容功能全部正常
