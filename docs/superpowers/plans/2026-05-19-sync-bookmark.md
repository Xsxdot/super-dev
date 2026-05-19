# Sync Bookmark Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 多分栏同步开始/停止书签，并支持按服务分块合并复制/导出。

**Architecture:** 在 `AppCore` 新增两个 `@Published` 状态（`syncGroupPanelIds`、`syncGroupIsRecording`）和 4 个方法，管理同步组的加入/退出和批量操作；`LogPanelView` 的书签控制区新增同步勾选框，读取 AppCore 状态决定行为，合并格式化逻辑在 UI 层完成。`LogBookmark` 模型不变。

**Tech Stack:** Swift, SwiftUI, XCTest (`@testable import SuperDev`)

---

## File Map

| 文件 | 改动类型 |
|------|---------|
| `SuperDev/AppCore/AppCore.swift` | 新增状态和 4 个方法 |
| `SuperDev/UI/MainWindow/LogPanelView.swift` | 书签控制区新增同步勾选，联动逻辑，合并复制/导出 |
| `SuperDev/SuperDevTests/SyncBookmarkTests.swift` | 新建，测试 AppCore 同步逻辑 |

---

### Task 1: AppCore — 同步组状态与方法

**Files:**
- Modify: `SuperDev/SuperDev/AppCore/AppCore.swift`
- Test: `SuperDev/SuperDevTests/SyncBookmarkTests.swift`

- [ ] **Step 1: 新建测试文件，写 toggleSyncGroup 的失败测试**

新建 `SuperDev/SuperDevTests/SyncBookmarkTests.swift`，内容如下：

```swift
import XCTest
@testable import SuperDev

@MainActor
final class SyncBookmarkTests: XCTestCase {

    func test_toggleSyncGroup_addsPanel() async {
        let core = AppCore()
        let panelId = UUID()
        core.toggleSyncGroup(panelId: panelId)
        XCTAssertTrue(core.syncGroupPanelIds.contains(panelId))
    }

    func test_toggleSyncGroup_removesPanelIfAlreadyIn() async {
        let core = AppCore()
        let panelId = UUID()
        core.toggleSyncGroup(panelId: panelId)
        core.toggleSyncGroup(panelId: panelId)
        XCTAssertFalse(core.syncGroupPanelIds.contains(panelId))
    }

    func test_startSyncBookmark_setsRecordingAndCreatesBookmarks() async {
        let core = AppCore()
        let p1 = UUID(), p2 = UUID()
        let s1 = UUID(), s2 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.toggleSyncGroup(panelId: p2)
        core.startSyncBookmark(serviceIdByPanelId: [p1: s1, p2: s2])
        XCTAssertTrue(core.syncGroupIsRecording)
        XCTAssertNotNil(core.bookmarks[p1])
        XCTAssertNotNil(core.bookmarks[p2])
        XCTAssertTrue(core.bookmarks[p1]!.isActive)
        XCTAssertTrue(core.bookmarks[p2]!.isActive)
    }

    func test_endSyncBookmark_completesAllBookmarksAndClearsRecording() async {
        let core = AppCore()
        let p1 = UUID(), p2 = UUID()
        let s1 = UUID(), s2 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.toggleSyncGroup(panelId: p2)
        core.startSyncBookmark(serviceIdByPanelId: [p1: s1, p2: s2])
        core.endSyncBookmark()
        XCTAssertFalse(core.syncGroupIsRecording)
        XCTAssertTrue(core.bookmarks[p1]!.isCompleted)
        XCTAssertTrue(core.bookmarks[p2]!.isCompleted)
    }

    func test_syncBookmarkFormattedText_groupsByServiceNameAlphabetically() async {
        let core = AppCore()
        let p1 = UUID(), p2 = UUID()
        let s1 = UUID(), s2 = UUID()
        core.toggleSyncGroup(panelId: p1)
        core.toggleSyncGroup(panelId: p2)
        core.startSyncBookmark(serviceIdByPanelId: [p1: s1, p2: s2])

        // 手动注入日志模拟书签锁定
        let ts = Date(timeIntervalSince1970: 3600)
        let entry1 = LogEntry(id: UUID(), timestamp: ts, serviceId: s1,
                              serviceName: "zebra", level: .info, message: "z-msg",
                              normalizedMessage: "z-msg", runId: UUID(), repeatCount: 1)
        let entry2 = LogEntry(id: UUID(), timestamp: ts, serviceId: s2,
                              serviceName: "alpha", level: .warn, message: "a-msg",
                              normalizedMessage: "a-msg", runId: UUID(), repeatCount: 1)
        core.bookmarks[p1]?.appendLog(entry1)
        core.bookmarks[p2]?.appendLog(entry2)
        core.endSyncBookmark()

        let text = core.syncBookmarkFormattedText()
        // alpha 应在 zebra 前面
        let alphaRange = text.range(of: "=== alpha")
        let zebraRange = text.range(of: "=== zebra")
        XCTAssertNotNil(alphaRange)
        XCTAssertNotNil(zebraRange)
        XCTAssertLessThan(alphaRange!.lowerBound, zebraRange!.lowerBound)
        XCTAssertTrue(text.contains("a-msg"))
        XCTAssertTrue(text.contains("z-msg"))
    }

    func test_syncBookmarkFormattedText_skipsPanelsWithNilServiceId() async {
        let core = AppCore()
        let p1 = UUID()
        core.toggleSyncGroup(panelId: p1)
        // serviceId = nil
        core.startSyncBookmark(serviceIdByPanelId: [p1: nil])
        core.endSyncBookmark()
        let text = core.syncBookmarkFormattedText()
        XCTAssertTrue(text.isEmpty)
    }
}
```

- [ ] **Step 2: 运行测试，确认编译失败（方法未定义）**

```bash
cd /Users/xushixin/workspace/super-debug/SuperDev
xcodebuild test -scheme SuperDev -destination 'platform=macOS' -only-testing:SuperDevTests/SyncBookmarkTests 2>&1 | tail -20
```

期望：编译错误，`value of type 'AppCore' has no member 'syncGroupPanelIds'` 等。

- [ ] **Step 3: 在 AppCore.swift 新增状态和方法**

在 `AppCore.swift` 的 `@Published var bookmarks` 行之后新增两个状态属性：

```swift
    /// 已勾选加入同步组的 panelId 集合
    @Published var syncGroupPanelIds: Set<UUID> = []
    /// 同步组是否正在录制中
    @Published var syncGroupIsRecording: Bool = false
```

在文件末尾 `// MARK: - Log Bookmarks` section 结束处（`clearBookmark` 方法之后，最后一个 `}` 之前）新增：

```swift
    // MARK: - Sync Bookmark

    // toggleSyncGroup 将 panelId 加入或移出同步组。
    //
    // 参数：
    //   - panelId: 目标面板 ID
    func toggleSyncGroup(panelId: UUID) {
        if syncGroupPanelIds.contains(panelId) {
            syncGroupPanelIds.remove(panelId)
        } else {
            syncGroupPanelIds.insert(panelId)
        }
    }

    // startSyncBookmark 对所有 syncGroupPanelIds 中的面板同时开始书签录制。
    //
    // 参数：
    //   - serviceIdByPanelId: 每个面板对应的服务 ID（可为 nil）
    func startSyncBookmark(serviceIdByPanelId: [UUID: UUID?]) {
        guard !syncGroupIsRecording else { return }
        for panelId in syncGroupPanelIds {
            let serviceId = serviceIdByPanelId[panelId] ?? nil
            startBookmark(panelId: panelId, serviceId: serviceId)
        }
        syncGroupIsRecording = true
    }

    // endSyncBookmark 对所有 syncGroupPanelIds 中的面板同时结束书签录制。
    func endSyncBookmark() {
        for panelId in syncGroupPanelIds {
            endBookmark(panelId: panelId)
        }
        syncGroupIsRecording = false
    }

    // syncBookmarkFormattedText 返回同步组内所有已完成书签的合并文本。
    //
    // 格式：按 serviceName 字母序分块，每块以 "=== <serviceName> ===" 为标题。
    // serviceId 为 nil 的面板跳过。
    //
    // 返回：合并后的纯文本字符串
    func syncBookmarkFormattedText() -> String {
        // 收集有服务的已完成书签，按 serviceName 分组
        var blocksByService: [(name: String, text: String)] = []
        for panelId in syncGroupPanelIds {
            guard let bm = bookmarks[panelId],
                  bm.isCompleted,
                  bm.serviceId != nil,
                  !bm.lockedLogs.isEmpty else { continue }
            let serviceName = bm.lockedLogs.first?.serviceName ?? "unknown"
            blocksByService.append((name: serviceName, text: bm.formattedText))
        }
        guard !blocksByService.isEmpty else { return "" }
        return blocksByService
            .sorted { $0.name < $1.name }
            .map { "=== \($0.name) ===\n\($0.text)" }
            .joined(separator: "\n\n")
    }
```

- [ ] **Step 4: 运行测试，确认全部通过**

```bash
cd /Users/xushixin/workspace/super-debug/SuperDev
xcodebuild test -scheme SuperDev -destination 'platform=macOS' -only-testing:SuperDevTests/SyncBookmarkTests 2>&1 | tail -30
```

期望：`** TEST SUCCEEDED **`，所有 5 个测试通过。

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add SuperDev/SuperDev/AppCore/AppCore.swift SuperDev/SuperDevTests/SyncBookmarkTests.swift
git commit -m "feat: add sync bookmark group state and methods to AppCore"
```

---

### Task 2: LogPanelView — 同步勾选框与联动

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift`

- [ ] **Step 1: 在 bookmarkControl 中加入同步勾选框**

找到 `LogPanelView.swift` 的 `bookmarkControl` computed var（约第 381 行），在整个 `Group { ... }` 外层包一个 `HStack`，在左侧插入同步勾选框。

将现有：

```swift
    private var bookmarkControl: some View {
        Group {
```

替换为：

```swift
    private var bookmarkControl: some View {
        HStack(spacing: 6) {
            syncToggle
            Group {
```

并在 `Group` 的最后一个 `}` 后（即 `bookmarkControl` 的最后一个 `}` 前）关闭 `HStack`：

找到 `bookmarkControl` 结尾处（`}` 前的最后一个 `}`），在其前面插入一个 `}`：

```swift
            }   // end Group
        }       // end HStack
    }           // end bookmarkControl
```

然后在 `bookmarkControl` 之前新增 `syncToggle` computed var（放在 `// MARK: - Toolbar` 区域末尾，`bookmarkControl` 之前）：

```swift
    private var syncToggle: some View {
        let inSync = core.syncGroupPanelIds.contains(panelId)
        return Button {
            core.toggleSyncGroup(panelId: panelId)
        } label: {
            HStack(spacing: 3) {
                Image(systemName: inSync ? "checkmark.square.fill" : "square")
                    .font(.system(size: 11))
                    .foregroundColor(inSync ? Theme.accent : .secondary)
                Text("同步")
                    .font(.system(size: 11))
                    .foregroundColor(inSync ? Theme.accent : .secondary)
            }
        }
        .buttonStyle(.plain)
        .help(inSync ? "退出同步录制组" : "加入同步录制组")
    }
```

- [ ] **Step 2: 修改 bookmarkControl 内的开始/停止按钮联动逻辑**

在 `bookmarkControl` 的 `Group` 内，现有的 idle 状态按钮（点击调用 `core.startBookmark`）需要区分是否在同步组中。

找到：

```swift
            } else {
                Button {
                    core.startBookmark(panelId: panelId, serviceId: serviceId)
                } label: {
                    Image(systemName: "record.circle")
                        .font(.system(size: 14))
                        .foregroundColor(.green)
                }
                .buttonStyle(.plain)
                .help("开始书签标记 (⌘⇧B)")
                .keyboardShortcut("b", modifiers: [.command, .shift])
            }
```

替换为：

```swift
            } else {
                Button {
                    if core.syncGroupPanelIds.contains(panelId) {
                        // 触发同步组所有分栏同时开始
                        let serviceIdByPanel = Dictionary(
                            uniqueKeysWithValues: core.syncGroupPanelIds.map { pid in
                                (pid, pid == panelId ? serviceId : nil as UUID?)
                            }
                        )
                        core.startSyncBookmark(serviceIdByPanelId: serviceIdByPanel)
                    } else {
                        core.startBookmark(panelId: panelId, serviceId: serviceId)
                    }
                } label: {
                    Image(systemName: "record.circle")
                        .font(.system(size: 14))
                        .foregroundColor(core.syncGroupPanelIds.contains(panelId) ? .blue : .green)
                }
                .buttonStyle(.plain)
                .help(core.syncGroupPanelIds.contains(panelId) ? "同步开始所有分栏书签 (⌘⇧B)" : "开始书签标记 (⌘⇧B)")
                .keyboardShortcut("b", modifiers: [.command, .shift])
            }
```

- [ ] **Step 3: 修改录制中状态的停止按钮联动逻辑**

找到 active 状态的停止按钮：

```swift
                    Button {
                        core.endBookmark(panelId: panelId)
                    } label: {
                        Image(systemName: "stop.circle.fill")
                            .font(.system(size: 14))
                            .foregroundColor(.red)
                    }
                    .buttonStyle(.plain)
                    .help("结束书签标记 (⌘⇧B)")
                    .keyboardShortcut("b", modifiers: [.command, .shift])
```

替换为：

```swift
                    Button {
                        if core.syncGroupIsRecording && core.syncGroupPanelIds.contains(panelId) {
                            core.endSyncBookmark()
                        } else {
                            core.endBookmark(panelId: panelId)
                        }
                    } label: {
                        Image(systemName: "stop.circle.fill")
                            .font(.system(size: 14))
                            .foregroundColor(.red)
                    }
                    .buttonStyle(.plain)
                    .help(core.syncGroupIsRecording && core.syncGroupPanelIds.contains(panelId) ? "同步结束所有分栏书签 (⌘⇧B)" : "结束书签标记 (⌘⇧B)")
                    .keyboardShortcut("b", modifiers: [.command, .shift])
```

- [ ] **Step 4: 修改已完成状态的复制/导出按钮——同步组显示合并操作**

找到已完成状态的复制按钮（调用 `bm.formattedText` 的那个）：

```swift
            if let bm = bookmark, bm.isCompleted {
                HStack(spacing: 6) {
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(bm.formattedText, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help("复制标记区间日志")

                    Button {
                        exportBookmark(bm)
                    } label: {
                        Image(systemName: "square.and.arrow.up")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help("导出标记区间日志")

                    Button {
                        core.clearBookmark(panelId: panelId)
                    } label: {
                        Image(systemName: "xmark.circle")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("清除书签")
                }
```

替换为：

```swift
            if let bm = bookmark, bm.isCompleted {
                let inSyncGroup = core.syncGroupPanelIds.contains(panelId)
                HStack(spacing: 6) {
                    Button {
                        let text = inSyncGroup
                            ? core.syncBookmarkFormattedText()
                            : bm.formattedText
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(text, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help(inSyncGroup ? "复制所有同步分栏日志（按服务分块）" : "复制标记区间日志")

                    Button {
                        if inSyncGroup {
                            exportSyncBookmark()
                        } else {
                            exportBookmark(bm)
                        }
                    } label: {
                        Image(systemName: "square.and.arrow.up")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.plain)
                    .help(inSyncGroup ? "导出所有同步分栏日志（按服务分块）" : "导出标记区间日志")

                    Button {
                        core.clearBookmark(panelId: panelId)
                    } label: {
                        Image(systemName: "xmark.circle")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("清除书签")
                }
```

- [ ] **Step 5: 新增 exportSyncBookmark 方法**

在 `LogPanelView.swift` 的 `exportBookmark` 方法之后新增：

```swift
    private func exportSyncBookmark() {
        let text = core.syncBookmarkFormattedText()
        guard !text.isEmpty else { return }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "superdev-sync-\(Int(Date().timeIntervalSince1970)).log"
        panel.allowedContentTypes = [.plainText]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            try? text.write(to: url, atomically: true, encoding: .utf8)
        }
    }
```

- [ ] **Step 6: 编译验证**

```bash
cd /Users/xushixin/workspace/super-debug/SuperDev
xcodebuild build -scheme SuperDev -destination 'platform=macOS' 2>&1 | tail -20
```

期望：`** BUILD SUCCEEDED **`，无编译错误。

- [ ] **Step 7: 提交**

```bash
cd /Users/xushixin/workspace/super-debug
git add SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift
git commit -m "feat: add sync bookmark toggle and coordinated start/stop/export to LogPanelView"
```

---

### Task 3: 完整测试套件验证

**Files:**
- Test: `SuperDev/SuperDevTests/SyncBookmarkTests.swift`（已存在）

- [ ] **Step 1: 运行全部测试确认无回归**

```bash
cd /Users/xushixin/workspace/super-debug/SuperDev
xcodebuild test -scheme SuperDev -destination 'platform=macOS' 2>&1 | grep -E "Test Suite|PASSED|FAILED|error:"
```

期望：所有 test suite 通过，无 FAILED，无新 error。

- [ ] **Step 2: 若有失败，修复并重新运行**

如果出现 `FAILED`，查看完整输出定位具体测试，修复后重新运行 Step 1。

- [ ] **Step 3: 最终提交（若 Task 2 有后续修复）**

```bash
cd /Users/xushixin/workspace/super-debug
git add -p
git commit -m "fix: address test failures from sync bookmark integration"
```

---

## 手工验证清单（构建后在 App 内测试）

1. 开两个分栏，各绑定不同服务
2. 两个分栏都勾选「同步」
3. 点任意一个分栏的「开始」→ 确认两个分栏都进入录制状态（红色停止按钮）
4. 点任意一个分栏的「停止」→ 确认两个分栏都完成
5. 点「复制」→ 粘贴到文本编辑器，确认按服务分块格式
6. 点「导出」→ 确认保存文件名为 `superdev-sync-<timestamp>.log`，内容分块正确
7. 只勾选一个分栏「同步」，另一个不勾选 → 另一个单独点开始/停止不影响同步组
8. 未勾选同步的分栏，书签行为与原来完全一致
