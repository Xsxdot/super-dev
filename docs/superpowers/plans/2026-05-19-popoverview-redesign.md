# PopoverView 样式重设计 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 PopoverView 重设计为 Pro Dark × Command Palette 风格，提升开源项目视觉吸引力。

**Architecture:** 新增 `Theme.swift` 集中管理所有颜色常量；完整重写 `PopoverView.swift` 的视图层，保留全部业务逻辑不变；左侧面板新增搜索过滤功能。

**Tech Stack:** SwiftUI, AppKit (NSColor), macOS 13+

---

## 文件结构

| 文件 | 操作 | 说明 |
|------|------|------|
| `SuperDev/UI/Theme.swift` | 新建 | Pro Dark 配色常量 |
| `SuperDev/UI/MenuBar/PopoverView.swift` | 完整重写 | 视图层，保留所有业务逻辑 |

---

## Task 1: 新建 Theme.swift — 配色常量

**Files:**
- Create: `SuperDev/SuperDev/UI/Theme.swift`

- [ ] **Step 1: 新建文件**

创建 `SuperDev/SuperDev/UI/Theme.swift`，写入以下内容：

```swift
import SwiftUI

enum Theme {
    // MARK: - Backgrounds
    static let bgPrimary    = Color(hex: "#0d1117")   // 左侧面板底色
    static let bgSecondary  = Color(hex: "#010409")   // 右侧面板底色
    static let bgElevated   = Color(hex: "#161b22")   // 选中行 / hover 背景
    static let bgToolbar    = Color(hex: "#0d1117")   // toolbar 行背景

    // MARK: - Borders
    static let borderPrimary   = Color(hex: "#21262d")
    static let borderSecondary = Color(hex: "#30363d")

    // MARK: - Text
    static let textPrimary   = Color(hex: "#e6edf3")
    static let textSecondary = Color(hex: "#8b949e")
    static let textTertiary  = Color(hex: "#6e7681")

    // MARK: - Accent
    static let accent = Color(hex: "#1f6feb")

    // MARK: - Status
    static let statusRunning  = Color(hex: "#3fb950")
    static let statusStarting = Color(hex: "#d29922")
    static let statusFailed   = Color(hex: "#f85149")
    static let statusStopped  = Color(hex: "#6e7681")
}

extension Color {
    init(hex: String) {
        let hex = hex.trimmingCharacters(in: CharacterSet.alphanumerics.inverted)
        var int: UInt64 = 0
        Scanner(string: hex).scanHexInt64(&int)
        let r = Double((int >> 16) & 0xFF) / 255
        let g = Double((int >> 8)  & 0xFF) / 255
        let b = Double(int         & 0xFF) / 255
        self.init(red: r, green: g, blue: b)
    }
}
```

- [ ] **Step 2: 编译验证**

在 Xcode 中按 ⌘B 编译。预期：0 errors，0 warnings（新文件本身无依赖）。

- [ ] **Step 3: Commit**

```bash
git add SuperDev/SuperDev/UI/Theme.swift
git commit -m "feat: add Theme color constants for Pro Dark style"
```

---

## Task 2: 重写左侧面板 — 搜索栏 + 项目/服务列表

**Files:**
- Modify: `SuperDev/SuperDev/UI/MenuBar/PopoverView.swift`

本 Task 只重写左侧面板（`projectList` 和 `projectRow`），右侧面板暂时保留原样。

- [ ] **Step 1: 在 PopoverView 顶部添加搜索 State**

在 `PopoverView` struct 内，`@State private var selectedServiceIds` 下方添加：

```swift
@State private var searchText: String = ""
```

- [ ] **Step 2: 替换 projectList 计算属性**

找到并替换整个 `private var projectList: some View` 计算属性（第 21-49 行）：

```swift
private var projectList: some View {
    VStack(spacing: 0) {
        // 搜索栏
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 10))
                .foregroundColor(Theme.textTertiary)
            TextField("搜索服务…", text: $searchText)
                .textFieldStyle(.plain)
                .font(.system(size: 10))
                .foregroundColor(Theme.textSecondary)
        }
        .padding(.horizontal, 9)
        .padding(.vertical, 5)
        .background(Theme.bgElevated)
        .overlay(
            RoundedRectangle(cornerRadius: 6)
                .stroke(Theme.borderSecondary, lineWidth: 1)
        )
        .cornerRadius(6)
        .padding(.horizontal, 10)
        .padding(.vertical, 10)

        Divider().background(Theme.borderPrimary)

        // 项目列表
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(core.projects) { project in
                    projectSection(project)
                }
            }
        }

        Divider().background(Theme.borderPrimary)

        // 添加项目
        Button {
            openAddProject()
        } label: {
            HStack(spacing: 5) {
                Image(systemName: "plus")
                    .font(.system(size: 11))
                Text("添加项目")
                    .font(.system(size: 10))
            }
            .foregroundColor(Theme.accent)
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
    .frame(width: 170)
    .background(Theme.bgPrimary)
}
```

- [ ] **Step 3: 添加 projectSection 方法**

在 `projectRow` 方法前插入新方法 `projectSection`：

```swift
private func projectSection(_ project: Project) -> some View {
    let filtered = filteredServices(of: project)
    guard !filtered.isEmpty || searchText.isEmpty else { return AnyView(EmptyView()) }

    return AnyView(VStack(alignment: .leading, spacing: 0) {
        // 项目 label
        HStack {
            Text(project.name.uppercased())
                .font(.system(size: 9, weight: .medium))
                .foregroundColor(Theme.textTertiary)
                .kerning(0.8)
            Spacer()
            Circle()
                .fill(projectStatusColor(project.overallStatus))
                .shadow(color: projectStatusColor(project.overallStatus).opacity(0.6),
                        radius: 3)
                .frame(width: 6, height: 6)
        }
        .padding(.horizontal, 10)
        .padding(.top, 8)
        .padding(.bottom, 3)

        // 服务行
        ForEach(filtered) { service in
            leftServiceRow(service, in: project)
        }
    })
}

private func filteredServices(of project: Project) -> [Service] {
    guard !searchText.isEmpty else { return project.services }
    return project.services.filter {
        $0.name.localizedCaseInsensitiveContains(searchText)
    }
}
```

- [ ] **Step 4: 添加 leftServiceRow 方法**

在 `projectSection` 方法后插入：

```swift
private func leftServiceRow(_ service: Service, in project: Project) -> some View {
    let isHovered = hoveredProjectId == project.id
    let isSelected = hoveredProjectId == project.id

    return HStack(spacing: 7) {
        Circle()
            .fill(serviceStatusColor(service.status))
            .shadow(color: serviceStatusColor(service.status).opacity(
                service.status == .running || service.status == .starting ? 0.6 : 0
            ), radius: 3)
            .frame(width: 6, height: 6)

        Text(service.name)
            .font(.system(size: 11))
            .foregroundColor(isSelected ? Theme.textPrimary : Theme.textSecondary)
            .lineLimit(1)

        Spacer()

        Text(statusLabel(service.status))
            .font(.system(size: 9))
            .foregroundColor(serviceStatusColor(service.status))
    }
    .padding(.horizontal, 10)
    .padding(.vertical, 5)
    .background(isSelected ? Theme.bgElevated : Color.clear)
    .overlay(alignment: .leading) {
        if isSelected {
            Rectangle()
                .frame(width: 2)
                .foregroundColor(Theme.accent)
        }
    }
    .contentShape(Rectangle())
    .onHover { hovered in
        if hovered {
            hoveredProjectId = project.id
            selectedServiceIds = Set(project.services.filter { $0.required }.map { $0.id })
        }
    }
}
```

- [ ] **Step 5: 添加颜色 helper**

找到现有的 `statusColor(_ status: ServiceStatus)` 方法，在其前方添加两个新方法（保留原方法不删，下一步用）：

```swift
private func serviceStatusColor(_ status: ServiceStatus) -> Color {
    switch status {
    case .stopped:  return Theme.statusStopped
    case .starting: return Theme.statusStarting
    case .running:  return Theme.statusRunning
    case .failed:   return Theme.statusFailed
    }
}

private func projectStatusColor(_ status: ProjectStatus) -> Color {
    switch status {
    case .stopped:  return Theme.statusStopped
    case .starting: return Theme.statusStarting
    case .running:  return Theme.statusRunning
    case .failed:   return Theme.statusFailed
    }
}
```

- [ ] **Step 6: 删除旧的 projectRow 方法**

删除整个 `private func projectRow(_ project: Project) -> some View` 方法（原第 51-80 行），因为已由 `projectSection` + `leftServiceRow` 替代。

- [ ] **Step 7: 编译验证**

⌘B 编译。预期：0 errors。如有 `projectRow` 引用残留报错，确认已完全删除该方法。

- [ ] **Step 8: Commit**

```bash
git add SuperDev/SuperDev/UI/MenuBar/PopoverView.swift
git commit -m "feat: redesign left panel with search bar and Pro Dark style"
```

---

## Task 3: 重写右侧面板 — Header + Toolbar + 服务列表 + Footer

**Files:**
- Modify: `SuperDev/SuperDev/UI/MenuBar/PopoverView.swift`

- [ ] **Step 1: 替换 servicePanel 方法**

找到并替换整个 `private func servicePanel(for project: Project) -> some View` 方法：

```swift
private func servicePanel(for project: Project) -> some View {
    VStack(spacing: 0) {
        // Header
        servicePanelHeader(for: project)

        Divider().background(Theme.borderPrimary)

        // Toolbar
        servicePanelToolbar(for: project)

        Divider().background(Color(hex: "#161b22"))

        // Service list
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                let required = project.services.filter { $0.required }
                let optional = project.services.filter { !$0.required }

                if !required.isEmpty {
                    serviceGroupLabel("必须启动")
                    ForEach(required) { service in
                        serviceRow(service, in: project)
                    }
                }

                if !optional.isEmpty {
                    serviceGroupLabel("可选")
                    ForEach(optional) { service in
                        serviceRow(service, in: project)
                    }
                }
            }
        }

        Divider().background(Theme.borderPrimary)

        // Footer
        servicePanelFooter()
    }
    .frame(width: 260)
    .background(Theme.bgSecondary)
}
```

- [ ] **Step 2: 添加 servicePanelHeader 方法**

在 `servicePanel` 方法后插入：

```swift
private func servicePanelHeader(for project: Project) -> some View {
    VStack(alignment: .leading, spacing: 6) {
        HStack(alignment: .center) {
            Text(project.name)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Theme.textPrimary)
            Spacer()
            // 全停按钮
            Button("全停") { core.stopAll(project: project) }
                .buttonStyle(.plain)
                .font(.system(size: 10))
                .foregroundColor(Theme.textSecondary)
                .padding(.horizontal, 9)
                .padding(.vertical, 3)
                .background(Theme.bgElevated)
                .overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.borderSecondary, lineWidth: 1))
                .cornerRadius(5)

            // 启动选中按钮
            Button("▶ 启动选中") {
                let toStart = project.services.filter { selectedServiceIds.contains($0.id) }
                core.startSelected(services: toStart, in: project)
            }
            .buttonStyle(.plain)
            .font(.system(size: 10, weight: .medium))
            .foregroundColor(.white)
            .padding(.horizontal, 9)
            .padding(.vertical, 3)
            .background(Theme.accent)
            .cornerRadius(5)
        }

        // 状态 badge 行
        HStack(spacing: 5) {
            let runningCount  = project.services.filter { $0.status == .running }.count
            let startingCount = project.services.filter { $0.status == .starting }.count
            let stoppedCount  = project.services.filter { $0.status == .stopped || $0.status == .failed }.count

            if runningCount > 0 {
                statusBadge("● \(runningCount) 运行中", color: Theme.statusRunning)
            }
            if startingCount > 0 {
                statusBadge("● \(startingCount) 启动中", color: Theme.statusStarting)
            }
            if stoppedCount > 0 {
                statusBadge("● \(stoppedCount) 停止", color: Theme.statusStopped)
            }
        }
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 9)
}

private func statusBadge(_ text: String, color: Color) -> some View {
    Text(text)
        .font(.system(size: 9))
        .foregroundColor(color)
        .padding(.horizontal, 7)
        .padding(.vertical, 1)
        .background(color.opacity(0.1))
        .overlay(RoundedRectangle(cornerRadius: 4).stroke(color.opacity(0.2), lineWidth: 1))
        .cornerRadius(4)
}
```

- [ ] **Step 3: 添加 servicePanelToolbar 方法**

在 `servicePanelHeader` 后插入：

```swift
private func servicePanelToolbar(for project: Project) -> some View {
    HStack(spacing: 10) {
        // 全选 checkbox
        Button {
            let allSelected = project.services.allSatisfy { selectedServiceIds.contains($0.id) }
            if allSelected {
                selectedServiceIds.removeAll()
            } else {
                project.services.forEach { selectedServiceIds.insert($0.id) }
            }
        } label: {
            let allSelected = project.services.allSatisfy { selectedServiceIds.contains($0.id) }
            let someSelected = project.services.contains { selectedServiceIds.contains($0.id) }
            ZStack {
                RoundedRectangle(cornerRadius: 2)
                    .fill(allSelected || someSelected ? Theme.accent : Color.clear)
                    .overlay(
                        RoundedRectangle(cornerRadius: 2)
                            .stroke(allSelected || someSelected ? Theme.accent : Theme.borderSecondary, lineWidth: 1)
                    )
                    .frame(width: 13, height: 13)
                if allSelected {
                    // 全选：白色实心方块
                    RoundedRectangle(cornerRadius: 1.5)
                        .fill(Color.white)
                        .frame(width: 8, height: 8)
                } else if someSelected {
                    // 部分选中：横线
                    Rectangle()
                        .fill(Color.white)
                        .frame(width: 8, height: 1.5)
                        .cornerRadius(1)
                }
            }
        }
        .buttonStyle(.plain)

        Text("全选")
            .font(.system(size: 10))
            .foregroundColor(Theme.textSecondary)

        Rectangle()
            .fill(Theme.borderPrimary)
            .frame(width: 1, height: 12)

        Button("反选") { toggleInvert(for: project) }
            .buttonStyle(.plain)
            .font(.system(size: 10))
            .foregroundColor(Theme.textSecondary)

        Spacer()
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 5)
    .background(Theme.bgToolbar)
}
```

- [ ] **Step 4: 替换 serviceGroupHeader 为 serviceGroupLabel**

删除旧的 `serviceGroupHeader` 方法，添加新的 `serviceGroupLabel`：

```swift
private func serviceGroupLabel(_ title: String) -> some View {
    Text(title)
        .font(.system(size: 9, weight: .medium))
        .foregroundColor(Theme.textTertiary)
        .kerning(0.6)
        .textCase(.uppercase)
        .padding(.horizontal, 12)
        .padding(.top, 8)
        .padding(.bottom, 3)
        .frame(maxWidth: .infinity, alignment: .leading)
}
```

- [ ] **Step 5: 替换 serviceRow 方法**

找到并替换整个 `private func serviceRow` 方法：

```swift
private func serviceRow(_ service: Service, in project: Project) -> some View {
    HStack(spacing: 8) {
        // 自绘 checkbox
        Button {
            if selectedServiceIds.contains(service.id) {
                selectedServiceIds.remove(service.id)
            } else {
                selectedServiceIds.insert(service.id)
            }
        } label: {
            let checked = selectedServiceIds.contains(service.id)
            ZStack {
                RoundedRectangle(cornerRadius: 2)
                    .fill(checked ? Theme.accent : Color.clear)
                    .overlay(
                        RoundedRectangle(cornerRadius: 2)
                            .stroke(checked ? Theme.accent : Theme.borderSecondary, lineWidth: 1)
                    )
                    .frame(width: 13, height: 13)
                if checked {
                    RoundedRectangle(cornerRadius: 1.5)
                        .fill(Color.white)
                        .frame(width: 8, height: 8)
                }
            }
        }
        .buttonStyle(.plain)

        // 状态点（运行/启动中加 glow）
        let statusColor = serviceStatusColor(service.status)
        let hasGlow = service.status == .running || service.status == .starting
        Circle()
            .fill(statusColor)
            .shadow(color: hasGlow ? statusColor.opacity(0.6) : .clear, radius: 3)
            .frame(width: 7, height: 7)

        // 服务名
        Text(service.name)
            .font(.system(size: 11))
            .foregroundColor(service.status == .stopped ? Theme.textTertiary : Theme.textPrimary)
            .lineLimit(1)

        Spacer()

        // 状态文字
        Text(statusLabel(service.status))
            .font(.system(size: 9))
            .foregroundColor(serviceStatusColor(service.status))

        // 启停按钮（18×18 圆角方块）
        Button {
            if service.status.isActive {
                core.stop(service, in: project)
            } else {
                core.start(service, in: project)
            }
        } label: {
            let isActive = service.status.isActive
            let btnColor = isActive ? serviceStatusColor(service.status) : Theme.bgElevated
            ZStack {
                RoundedRectangle(cornerRadius: 3)
                    .fill(btnColor)
                    .overlay(
                        RoundedRectangle(cornerRadius: 3)
                            .stroke(isActive ? btnColor : Theme.borderSecondary, lineWidth: 1)
                    )
                    .frame(width: 18, height: 18)
                Image(systemName: isActive ? "stop.fill" : "play.fill")
                    .font(.system(size: 7, weight: .bold))
                    .foregroundColor(isActive ? .black : Theme.textSecondary)
            }
        }
        .buttonStyle(.plain)
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 5)
}
```

- [ ] **Step 6: 替换 servicePanelFooter**

添加新的 footer 方法，删除原来内联在 `servicePanel` 里的 footer HStack：

```swift
private func servicePanelFooter() -> some View {
    HStack {
        Spacer()
        Button {
            openMainWindow()
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "text.alignleft")
                    .font(.system(size: 10))
                Text("查看日志")
                    .font(.system(size: 10))
            }
            .foregroundColor(Theme.accent)
        }
        .buttonStyle(.plain)
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 7)
}
```

- [ ] **Step 7: 更新 body 的 frame**

找到 body 里的 `.frame(minWidth: hoveredProject == nil ? 200 : 440, minHeight: 300)` 改为：

```swift
.frame(minWidth: hoveredProject == nil ? 170 : 430, minHeight: 300)
```

- [ ] **Step 8: 清理旧的 statusColor 方法**

原文件末尾有两个 `statusColor` overload（分别接受 `ServiceStatus` 和 `ProjectStatus`），这两个方法已由 `serviceStatusColor` / `projectStatusColor` 替代。删除这两个旧方法：

```swift
// 删除这两个方法：
private func statusColor(_ status: ServiceStatus) -> Color { ... }
private func statusColor(_ status: ProjectStatus) -> Color { ... }
```

- [ ] **Step 9: 编译验证**

⌘B 编译。预期：0 errors。常见报错及修复：
- `Color(hex:)` 找不到 → 确认 `Theme.swift` 已加入编译目标
- `serviceGroupHeader` 找不到 → 已改名为 `serviceGroupLabel`，确认所有调用都已更新

- [ ] **Step 10: 运行 app 手动验证**

在 Xcode 中运行 app（⌘R），点击 menubar 图标，验证：
1. PopoverView 弹出，左侧面板宽 170px，深色背景 `#0d1117`
2. 搜索栏可输入，服务名实时过滤
3. 服务状态点有 glow 效果（运行中为绿色光晕）
4. hover 到项目后右侧面板展示，宽 260px
5. 右侧 header 显示状态 badge（运行/启动中/停止数量）
6. 全选 checkbox 三态正常（空/部分/全选）
7. 启停按钮颜色跟随状态
8. 「查看日志」点击打开主窗口

- [ ] **Step 11: Commit**

```bash
git add SuperDev/SuperDev/UI/MenuBar/PopoverView.swift
git commit -m "feat: redesign right panel with status badges, custom checkboxes and start/stop buttons"
```

---

## Self-Review

**Spec coverage 检查：**
- ✅ 左侧 170px + 右侧 260px 布局
- ✅ 搜索栏（Task 2）
- ✅ 项目 label 旁状态点（Task 2 `projectSection`）
- ✅ 服务行 hover 选中态 + 左侧蓝色竖条（Task 2 `leftServiceRow`）
- ✅ 右侧 header：项目名 + 状态 badge + 全停/启动按钮（Task 3 `servicePanelHeader`）
- ✅ toolbar 行：全选（三态）+ 反选（Task 3 `servicePanelToolbar`）
- ✅ 服务行：自绘 checkbox + 状态点 glow + 状态文字 + 启停按钮（Task 3 `serviceRow`）
- ✅ footer：查看日志（Task 3 `servicePanelFooter`）
- ✅ Pro Dark 配色全部来自 Theme.swift（Task 1）

**Placeholder 扫描：** 无 TBD/TODO，所有步骤含完整代码。

**类型一致性：**
- `serviceStatusColor` 在 Task 2 Step 5 定义，Task 3 Step 5 使用 ✅
- `projectStatusColor` 在 Task 2 Step 5 定义，Task 2 Step 3 使用 ✅
- `serviceGroupLabel` 在 Task 3 Step 4 定义，Task 3 Step 1 调用 ✅
- `servicePanelHeader/Toolbar/Footer` 在 Task 3 各 Step 定义，Task 3 Step 1 调用 ✅
- `statusLabel` 沿用原文件方法，未改动 ✅
