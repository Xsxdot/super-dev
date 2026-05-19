# Split View & Log Bookmarks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add recursive split-panel layout (drag service from sidebar to panel edge) and per-panel log bookmarks (start/end marker with locked log memory and copy/export).

**Architecture:** A recursive `PanelLayout` enum replaces the single `serviceId` in `MainWindowView`; a new `PanelLayoutView` renders the tree and handles drop targets. `AppCore` gains a `bookmarks` dictionary keyed by panel ID; log ingestion pipes new entries into any active bookmark alongside the main buffer.

**Tech Stack:** SwiftUI (macOS), Swift 5.9+, XCTest

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `SuperDev/AppCore/Models/PanelLayout.swift` | **Create** | `PanelLayout` enum, mutating tree helpers |
| `SuperDev/AppCore/Models/LogBookmark.swift` | **Create** | `LogBookmark` struct |
| `SuperDev/AppCore/AppCore.swift` | **Modify** | Add `bookmarks` published dict + bookmark API; pipe new logs into active bookmarks |
| `SuperDev/UI/MainWindow/PanelLayoutView.swift` | **Create** | Recursive split renderer + drop-zone overlay |
| `SuperDev/UI/MainWindow/MainWindowView.swift` | **Modify** | Replace `selectedServiceId` + `LogPanelView` with `PanelLayout` state + `PanelLayoutView` |
| `SuperDev/UI/MainWindow/SidebarView.swift` | **Modify** | Add `.draggable(service.id)` to service rows |
| `SuperDev/UI/MainWindow/LogPanelView.swift` | **Modify** | Accept panel `id`; add bookmark toolbar controls; extend `makeLogDisplay()` for bookmark rendering |
| `SuperDevTests/PanelLayoutTests.swift` | **Create** | Unit tests for layout tree mutations |
| `SuperDevTests/LogBookmarkTests.swift` | **Create** | Unit tests for bookmark lifecycle |

---

## Task 1: PanelLayout model + tree helpers

**Files:**
- Create: `SuperDev/SuperDev/AppCore/Models/PanelLayout.swift`
- Create: `SuperDev/SuperDevTests/PanelLayoutTests.swift`

- [ ] **Step 1.1: Write the failing tests**

Create `SuperDev/SuperDevTests/PanelLayoutTests.swift`:

```swift
import XCTest
@testable import SuperDev

final class PanelLayoutTests: XCTestCase {

    // MARK: - id

    func test_leaf_hasStableId() {
        let id = UUID()
        let leaf = PanelLayout.leaf(id: id, serviceId: nil)
        XCTAssertEqual(leaf.id, id)
    }

    // MARK: - replacing a leaf with a split

    func test_splitLeaf_horizontal_right_producesCorrectTree() {
        let leafId = UUID()
        let serviceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.splitLeaf(id: leafId, axis: .horizontal, newServiceId: serviceId, newSide: .second)
        guard case .split(_, let axis, let ratio, let first, let second) = layout else {
            XCTFail("Expected split"); return
        }
        XCTAssertEqual(axis, .horizontal)
        XCTAssertEqual(ratio, 0.5, accuracy: 0.001)
        // original leaf is first
        if case .leaf(let fId, _) = first { XCTAssertEqual(fId, leafId) } else { XCTFail("first should be original leaf") }
        // new panel is second
        if case .leaf(_, let sid) = second { XCTAssertEqual(sid, serviceId) } else { XCTFail("second should be new leaf") }
    }

    func test_splitLeaf_horizontal_left_newPanelIsFirst() {
        let leafId = UUID()
        let serviceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.splitLeaf(id: leafId, axis: .horizontal, newServiceId: serviceId, newSide: .first)
        guard case .split(_, _, _, let first, let second) = layout else {
            XCTFail("Expected split"); return
        }
        if case .leaf(_, let sid) = first { XCTAssertEqual(sid, serviceId) } else { XCTFail("first should be new leaf") }
        if case .leaf(let fId, _) = second { XCTAssertEqual(fId, leafId) } else { XCTFail("second should be original leaf") }
    }

    // MARK: - replaceService

    func test_replaceService_updatesLeafServiceId() {
        let leafId = UUID()
        let newServiceId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.replaceService(panelId: leafId, newServiceId: newServiceId)
        if case .leaf(_, let sid) = layout {
            XCTAssertEqual(sid, newServiceId)
        } else {
            XCTFail("Should remain a leaf")
        }
    }

    // MARK: - removeLeaf

    func test_removeLeaf_fromSplit_promotesOtherChild() {
        let leftId = UUID()
        let rightId = UUID()
        let leftLeaf = PanelLayout.leaf(id: leftId, serviceId: nil)
        let rightLeaf = PanelLayout.leaf(id: rightId, serviceId: nil)
        var layout = PanelLayout.split(id: UUID(), axis: .horizontal, ratio: 0.5, first: leftLeaf, second: rightLeaf)
        layout.removeLeaf(id: leftId)
        // right child should be promoted to root
        if case .leaf(let id, _) = layout { XCTAssertEqual(id, rightId) } else { XCTFail("right child should be promoted") }
    }

    func test_removeLeaf_onlyLeaf_doesNothing() {
        let leafId = UUID()
        var layout = PanelLayout.leaf(id: leafId, serviceId: nil)
        layout.removeLeaf(id: leafId)
        // single leaf: can't remove, stays
        if case .leaf(let id, _) = layout { XCTAssertEqual(id, leafId) } else { XCTFail("leaf should remain") }
    }

    // MARK: - Codable round-trip

    func test_codable_roundTrip_singleLeaf() throws {
        let original = PanelLayout.leaf(id: UUID(), serviceId: UUID())
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        XCTAssertEqual(original.id, decoded.id)
    }

    func test_codable_roundTrip_split() throws {
        let original = PanelLayout.split(
            id: UUID(), axis: .horizontal, ratio: 0.4,
            first: .leaf(id: UUID(), serviceId: nil),
            second: .leaf(id: UUID(), serviceId: UUID())
        )
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PanelLayout.self, from: data)
        XCTAssertEqual(original.id, decoded.id)
        guard case .split(_, let axis, let ratio, _, _) = decoded else { XCTFail(); return }
        XCTAssertEqual(axis, .horizontal)
        XCTAssertEqual(ratio, 0.4, accuracy: 0.001)
    }
}
```

- [ ] **Step 1.2: Run tests — verify they fail**

In Xcode: Product → Test (⌘U), or run:
```
xcodebuild test -project SuperDev/SuperDev.xcodeproj -scheme SuperDev -destination 'platform=macOS' -only-testing SuperDevTests/PanelLayoutTests 2>&1 | tail -20
```
Expected: compile error — `PanelLayout` not found.

- [ ] **Step 1.3: Create PanelLayout.swift**

Create `SuperDev/SuperDev/AppCore/Models/PanelLayout.swift`:

```swift
// PanelLayout 描述日志面板的递归分割树。
//
// 职责：
//   - 表示单个叶子面板（leaf）或水平/垂直分割（split）
//   - 提供不可变树的变异辅助方法
//
// 边界：
//   - 纯数据模型，不持有 SwiftUI 状态
//   - 持久化由调用方负责（编码为 JSON 写入 UserDefaults）
import Foundation
import SwiftUI

enum PanelLayout: Codable, Identifiable {
    case leaf(id: UUID, serviceId: UUID?)
    case split(id: UUID, axis: Axis, ratio: CGFloat, first: PanelLayout, second: PanelLayout)

    var id: UUID {
        switch self {
        case .leaf(let id, _): return id
        case .split(let id, _, _, _, _): return id
        }
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case type, id, serviceId, axis, ratio, first, second
    }

    private enum LayoutType: String, Codable {
        case leaf, split
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let type = try c.decode(LayoutType.self, forKey: .type)
        let id = try c.decode(UUID.self, forKey: .id)
        switch type {
        case .leaf:
            let serviceId = try c.decodeIfPresent(UUID.self, forKey: .serviceId)
            self = .leaf(id: id, serviceId: serviceId)
        case .split:
            let axis = try c.decode(Axis.self, forKey: .axis)
            let ratio = try c.decode(CGFloat.self, forKey: .ratio)
            let first = try c.decode(PanelLayout.self, forKey: .first)
            let second = try c.decode(PanelLayout.self, forKey: .second)
            self = .split(id: id, axis: axis, ratio: ratio, first: first, second: second)
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .leaf(let id, let serviceId):
            try c.encode(LayoutType.leaf, forKey: .type)
            try c.encode(id, forKey: .id)
            try c.encodeIfPresent(serviceId, forKey: .serviceId)
        case .split(let id, let axis, let ratio, let first, let second):
            try c.encode(LayoutType.split, forKey: .type)
            try c.encode(id, forKey: .id)
            try c.encode(axis, forKey: .axis)
            try c.encode(ratio, forKey: .ratio)
            try c.encode(first, forKey: .first)
            try c.encode(second, forKey: .second)
        }
    }

    // MARK: - Tree mutations

    /// 把 id 对应的叶子替换为一个分割节点，原叶子保留在 originalSide，新面板在另一侧。
    mutating func splitLeaf(id leafId: UUID, axis: Axis, newServiceId: UUID?, newSide: SplitSide) {
        switch self {
        case .leaf(let id, let serviceId):
            guard id == leafId else { return }
            let newLeaf = PanelLayout.leaf(id: UUID(), serviceId: newServiceId)
            let original = PanelLayout.leaf(id: id, serviceId: serviceId)
            switch newSide {
            case .first:
                self = .split(id: UUID(), axis: axis, ratio: 0.5, first: newLeaf, second: original)
            case .second:
                self = .split(id: UUID(), axis: axis, ratio: 0.5, first: original, second: newLeaf)
            }
        case .split(let id, let axis2, let ratio, var first, var second):
            first.splitLeaf(id: leafId, axis: axis, newServiceId: newServiceId, newSide: newSide)
            second.splitLeaf(id: leafId, axis: axis, newServiceId: newServiceId, newSide: newSide)
            self = .split(id: id, axis: axis2, ratio: ratio, first: first, second: second)
        }
    }

    /// 把 id 对应的叶子的服务替换为 newServiceId（不分割）。
    mutating func replaceService(panelId: UUID, newServiceId: UUID?) {
        switch self {
        case .leaf(let id, _):
            if id == panelId { self = .leaf(id: id, serviceId: newServiceId) }
        case .split(let id, let axis, let ratio, var first, var second):
            first.replaceService(panelId: panelId, newServiceId: newServiceId)
            second.replaceService(panelId: panelId, newServiceId: newServiceId)
            self = .split(id: id, axis: axis, ratio: ratio, first: first, second: second)
        }
    }

    /// 从树中移除指定叶子，兄弟节点提升替代父节点。
    /// 如果根节点本身是目标叶子则不做任何事（至少保留一个面板）。
    mutating func removeLeaf(id leafId: UUID) {
        guard case .split(_, _, _, let first, let second) = self else { return }
        if case .leaf(let fId, _) = first, fId == leafId {
            self = second; return
        }
        if case .leaf(let sId, _) = second, sId == leafId {
            self = first; return
        }
        if case .split(let id, let axis, let ratio, var f, var s) = self {
            f.removeLeaf(id: leafId)
            s.removeLeaf(id: leafId)
            self = .split(id: id, axis: axis, ratio: ratio, first: f, second: s)
        }
    }

    // MARK: - Query

    /// 收集所有叶子节点的 panelId。
    var allLeafIds: [UUID] {
        switch self {
        case .leaf(let id, _): return [id]
        case .split(_, _, _, let first, let second): return first.allLeafIds + second.allLeafIds
        }
    }
}

enum SplitSide: String, Codable {
    case first, second
}

// Axis Codable support (SwiftUI.Axis is not Codable by default)
extension Axis: Codable {
    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(Int.self)
        self = raw == 0 ? .horizontal : .vertical
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(self == .horizontal ? 0 : 1)
    }
}
```

- [ ] **Step 1.4: Run tests — verify they pass**

```
xcodebuild test -project SuperDev/SuperDev.xcodeproj -scheme SuperDev -destination 'platform=macOS' -only-testing SuperDevTests/PanelLayoutTests 2>&1 | tail -20
```
Expected: All 8 tests PASS.

- [ ] **Step 1.5: Commit**

```bash
git add SuperDev/SuperDev/AppCore/Models/PanelLayout.swift SuperDev/SuperDevTests/PanelLayoutTests.swift
git commit -m "feat: add PanelLayout recursive model with tree mutation helpers"
```

---

## Task 2: LogBookmark model + AppCore bookmark API

**Files:**
- Create: `SuperDev/SuperDev/AppCore/Models/LogBookmark.swift`
- Modify: `SuperDev/SuperDev/AppCore/AppCore.swift`
- Create: `SuperDev/SuperDevTests/LogBookmarkTests.swift`

- [ ] **Step 2.1: Write the failing tests**

Create `SuperDev/SuperDevTests/LogBookmarkTests.swift`:

```swift
import XCTest
@testable import SuperDev

final class LogBookmarkTests: XCTestCase {

    private func makeEntry(serviceId: UUID = UUID(), message: String = "msg") -> LogEntry {
        LogEntry(
            id: UUID(), timestamp: Date(), serviceId: serviceId,
            serviceName: "svc", level: .info, message: message,
            normalizedMessage: message, runId: UUID(), repeatCount: 1
        )
    }

    // MARK: - Initial state

    func test_bookmark_initialState_noTimestamps() {
        let bm = LogBookmark(panelId: UUID())
        XCTAssertNil(bm.startTime)
        XCTAssertNil(bm.endTime)
        XCTAssertTrue(bm.lockedLogs.isEmpty)
    }

    // MARK: - isActive

    func test_isActive_trueWhenStartedNoEnd() {
        var bm = LogBookmark(panelId: UUID())
        bm.startTime = Date()
        XCTAssertTrue(bm.isActive)
    }

    func test_isActive_falseWhenCompleted() {
        var bm = LogBookmark(panelId: UUID())
        bm.startTime = Date()
        bm.endTime = Date()
        XCTAssertFalse(bm.isActive)
    }

    func test_isActive_falseWhenNoStart() {
        let bm = LogBookmark(panelId: UUID())
        XCTAssertFalse(bm.isActive)
    }

    // MARK: - isCompleted

    func test_isCompleted_trueWhenBothTimestampsSet() {
        var bm = LogBookmark(panelId: UUID())
        bm.startTime = Date()
        bm.endTime = Date()
        XCTAssertTrue(bm.isCompleted)
    }

    // MARK: - appendLog

    func test_appendLog_whenActive_addsToLockedLogs() {
        var bm = LogBookmark(panelId: UUID())
        bm.startTime = Date()
        let entry = makeEntry()
        bm.appendLog(entry)
        XCTAssertEqual(bm.lockedLogs.count, 1)
        XCTAssertEqual(bm.lockedLogs[0].id, entry.id)
    }

    func test_appendLog_whenCompleted_doesNotAdd() {
        var bm = LogBookmark(panelId: UUID())
        bm.startTime = Date()
        bm.endTime = Date()
        bm.appendLog(makeEntry())
        XCTAssertTrue(bm.lockedLogs.isEmpty)
    }

    func test_appendLog_whenNoStart_doesNotAdd() {
        var bm = LogBookmark(panelId: UUID())
        bm.appendLog(makeEntry())
        XCTAssertTrue(bm.lockedLogs.isEmpty)
    }

    // MARK: - formattedText

    func test_formattedText_formatsCorrectly() {
        var bm = LogBookmark(panelId: UUID())
        bm.startTime = Date()
        let ts = Date(timeIntervalSince1970: 0)
        let entry = LogEntry(
            id: UUID(), timestamp: ts, serviceId: UUID(),
            serviceName: "api", level: .error, message: "boom",
            normalizedMessage: "boom", runId: UUID(), repeatCount: 1
        )
        bm.appendLog(entry)
        let text = bm.formattedText
        XCTAssertTrue(text.contains("[api]"))
        XCTAssertTrue(text.contains("ERROR"))
        XCTAssertTrue(text.contains("boom"))
    }
}
```

- [ ] **Step 2.2: Run tests — verify they fail**

```
xcodebuild test -project SuperDev/SuperDev.xcodeproj -scheme SuperDev -destination 'platform=macOS' -only-testing SuperDevTests/LogBookmarkTests 2>&1 | tail -20
```
Expected: compile error — `LogBookmark` not found.

- [ ] **Step 2.3: Create LogBookmark.swift**

Create `SuperDev/SuperDev/AppCore/Models/LogBookmark.swift`:

```swift
// LogBookmark 保存单个面板的日志标记区间。
//
// 职责：
//   - 记录开始/结束时间戳
//   - 独立于主缓冲区锁定日志条目
//   - 提供格式化文本用于复制/导出
//
// 边界：
//   - 纯内存，不持久化到磁盘
//   - 每个面板最多一个活跃书签
import Foundation

struct LogBookmark {
    let panelId: UUID
    var startTime: Date?
    var endTime: Date?
    var lockedLogs: [LogEntry] = []

    init(panelId: UUID) {
        self.panelId = panelId
    }

    /// 已开始且尚未结束。
    var isActive: Bool { startTime != nil && endTime == nil }

    /// 已开始且已结束。
    var isCompleted: Bool { startTime != nil && endTime != nil }

    /// 仅当 isActive 时追加日志；其余状态忽略。
    mutating func appendLog(_ entry: LogEntry) {
        guard isActive else { return }
        lockedLogs.append(entry)
    }

    /// 格式化所有锁定日志为纯文本，用于复制/导出。
    var formattedText: String {
        lockedLogs.map { entry in
            let time = entry.timestamp.formatted(.dateTime.hour().minute().second())
            return "\(time) [\(entry.serviceName)] \(entry.level.rawValue) \(entry.message)"
        }.joined(separator: "\n")
    }
}
```

- [ ] **Step 2.4: Add bookmark API to AppCore.swift**

In `SuperDev/SuperDev/AppCore/AppCore.swift`:

**2.4a** — Add `@Published var bookmarks` property after `hiddenServiceIds`:

```swift
    @Published var bookmarks: [UUID: LogBookmark] = [:]
```

**2.4b** — Add bookmark methods at the end of AppCore, before the final `}`:

```swift
    // MARK: - Log Bookmarks

    func startBookmark(panelId: UUID) {
        bookmarks[panelId] = LogBookmark(panelId: panelId)
        bookmarks[panelId]?.startTime = Date()
    }

    func endBookmark(panelId: UUID) {
        bookmarks[panelId]?.endTime = Date()
    }

    func clearBookmark(panelId: UUID) {
        bookmarks.removeValue(forKey: panelId)
    }
```

**2.4c** — In the `onLog` closure inside `getOrCreateManager(for:)`, add bookmark piping **after** `self.logStore.append(entry)`:

Find this block (around line 326–329):
```swift
            onLog: { [weak self] serviceId, serviceName, line in
                guard let self else { return }
                let entry = self.logEngine.parseLine(line, serviceId: serviceId, serviceName: serviceName)
                self.logEngine.ingest(entry, into: &self.logs)
                self.logStore.append(entry)
            },
```

Replace with:
```swift
            onLog: { [weak self] serviceId, serviceName, line in
                guard let self else { return }
                let entry = self.logEngine.parseLine(line, serviceId: serviceId, serviceName: serviceName)
                self.logEngine.ingest(entry, into: &self.logs)
                self.logStore.append(entry)
                // 把新日志追加到所有活跃书签
                for panelId in self.bookmarks.keys where self.bookmarks[panelId]?.isActive == true {
                    self.bookmarks[panelId]?.appendLog(entry)
                }
            },
```

- [ ] **Step 2.5: Run tests — verify they pass**

```
xcodebuild test -project SuperDev/SuperDev.xcodeproj -scheme SuperDev -destination 'platform=macOS' -only-testing SuperDevTests/LogBookmarkTests 2>&1 | tail -20
```
Expected: All 9 tests PASS.

- [ ] **Step 2.6: Commit**

```bash
git add SuperDev/SuperDev/AppCore/Models/LogBookmark.swift SuperDev/SuperDev/AppCore/AppCore.swift SuperDev/SuperDevTests/LogBookmarkTests.swift
git commit -m "feat: add LogBookmark model and AppCore bookmark API"
```

---

## Task 3: SidebarView — make service rows draggable

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/SidebarView.swift`

- [ ] **Step 3.1: Add draggable modifier to service rows**

In `SidebarView.swift`, the service `HStack` inside `ForEach(project.services)` currently ends with `.tag(Optional(service.id))`. Add `.draggable` before `.tag`:

Replace:
```swift
                    ForEach(project.services) { service in
                        HStack {
                            Circle()
                                .fill(serviceStatusColor(service.status))
                                .frame(width: 7, height: 7)
                            Text(service.name)
                        }
                        .tag(Optional(service.id))
                    }
```

With:
```swift
                    ForEach(project.services) { service in
                        HStack {
                            Circle()
                                .fill(serviceStatusColor(service.status))
                                .frame(width: 7, height: 7)
                            Text(service.name)
                        }
                        .draggable(service.id.uuidString)
                        .tag(Optional(service.id))
                    }
```

- [ ] **Step 3.2: Build — verify it compiles**

In Xcode: Product → Build (⌘B). Expected: Build Succeeded.

- [ ] **Step 3.3: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/SidebarView.swift
git commit -m "feat: make sidebar service rows draggable"
```

---

## Task 4: PanelLayoutView — recursive renderer + drop zones

**Files:**
- Create: `SuperDev/SuperDev/UI/MainWindow/PanelLayoutView.swift`

- [ ] **Step 4.1: Create PanelLayoutView.swift**

Create `SuperDev/SuperDev/UI/MainWindow/PanelLayoutView.swift`:

```swift
// PanelLayoutView 递归渲染 PanelLayout 树。
//
// 职责：
//   - leaf 节点渲染 LogPanelView + 拖放覆盖层
//   - split 节点渲染 HSplitView 或 VSplitView，递归渲染子节点
//   - 处理从侧边栏拖入服务 UUID 的 drop 事件
//
// 边界：
//   - 不直接修改 AppCore，所有布局变更通过 binding 回调完成
//   - drop zone 逻辑纯 SwiftUI，不依赖 AppKit
import SwiftUI

struct PanelLayoutView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout

    var body: some View {
        layoutView(node: $layout)
    }

    @ViewBuilder
    private func layoutView(node: Binding<PanelLayout>) -> some View {
        switch node.wrappedValue {
        case .leaf:
            LeafPanelView(layout: node)
                .environmentObject(core)
        case .split(_, let axis, _, _, _):
            if axis == .horizontal {
                HSplitView {
                    layoutView(node: firstBinding(node))
                    layoutView(node: secondBinding(node))
                }
            } else {
                VSplitView {
                    layoutView(node: firstBinding(node))
                    layoutView(node: secondBinding(node))
                }
            }
        }
    }

    private func firstBinding(_ node: Binding<PanelLayout>) -> Binding<PanelLayout> {
        Binding(
            get: {
                if case .split(_, _, _, let f, _) = node.wrappedValue { return f }
                return node.wrappedValue
            },
            set: { newFirst in
                if case .split(let id, let axis, let ratio, _, let s) = node.wrappedValue {
                    node.wrappedValue = .split(id: id, axis: axis, ratio: ratio, first: newFirst, second: s)
                }
            }
        )
    }

    private func secondBinding(_ node: Binding<PanelLayout>) -> Binding<PanelLayout> {
        Binding(
            get: {
                if case .split(_, _, _, _, let s) = node.wrappedValue { return s }
                return node.wrappedValue
            },
            set: { newSecond in
                if case .split(let id, let axis, let ratio, let f, _) = node.wrappedValue {
                    node.wrappedValue = .split(id: id, axis: axis, ratio: ratio, first: f, second: newSecond)
                }
            }
        )
    }
}

// MARK: - LeafPanelView

private struct LeafPanelView: View {
    @EnvironmentObject var core: AppCore
    @Binding var layout: PanelLayout
    @State private var dropHighlight: DropEdge? = nil

    private var panelId: UUID {
        if case .leaf(let id, _) = layout { return id }
        return UUID()
    }

    private var serviceId: UUID? {
        if case .leaf(_, let sid) = layout { return sid }
        return nil
    }

    private var project: Project? {
        core.project(forServiceId: serviceId)
    }

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                panelHeader
                LogPanelView(panelId: panelId, serviceId: serviceId, project: project)
                    .environmentObject(core)
            }

            // 拖放覆盖层
            GeometryReader { geo in
                let w = geo.size.width
                let h = geo.size.height
                let edgeFraction: CGFloat = 0.20

                ZStack {
                    // 左边缘
                    dropZone(edge: .left)
                        .frame(width: w * edgeFraction, height: h)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    // 右边缘
                    dropZone(edge: .right)
                        .frame(width: w * edgeFraction, height: h)
                        .frame(maxWidth: .infinity, alignment: .trailing)
                    // 上边缘
                    dropZone(edge: .top)
                        .frame(width: w, height: h * edgeFraction)
                        .frame(maxHeight: .infinity, alignment: .top)
                    // 下边缘
                    dropZone(edge: .bottom)
                        .frame(width: w, height: h * edgeFraction)
                        .frame(maxHeight: .infinity, alignment: .bottom)
                    // 中心（替换服务）
                    dropZone(edge: nil)
                        .frame(width: w * 0.6, height: h * 0.6)
                }
            }
        }
    }

    private var panelHeader: some View {
        HStack {
            Text(headerTitle)
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(.secondary)
                .lineLimit(1)
            Spacer()
            Button {
                layout.removeLeaf(id: panelId)
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 9))
            }
            .buttonStyle(.plain)
            .help("关闭此面板")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(Color(NSColor.controlBackgroundColor))
    }

    private var headerTitle: String {
        guard let sid = serviceId else { return "未选择" }
        for project in core.projects {
            if let svc = project.services.first(where: { $0.id == sid }) {
                return svc.name
            }
        }
        return "未选择"
    }

    @ViewBuilder
    private func dropZone(edge: DropEdge?) -> some View {
        let isHighlighted = dropHighlight == edge
        Color.clear
            .contentShape(Rectangle())
            .overlay(
                isHighlighted
                    ? RoundedRectangle(cornerRadius: 4)
                        .fill(Color.accentColor.opacity(0.25))
                        .overlay(RoundedRectangle(cornerRadius: 4).stroke(Color.accentColor, lineWidth: 2))
                    : nil
            )
            .dropDestination(for: String.self) { items, _ in
                guard let uuidString = items.first,
                      let droppedServiceId = UUID(uuidString: uuidString) else { return false }
                handleDrop(serviceId: droppedServiceId, edge: edge)
                dropHighlight = nil
                return true
            } isTargeted: { targeted in
                dropHighlight = targeted ? edge : (dropHighlight == edge ? nil : dropHighlight)
            }
    }

    private func handleDrop(serviceId droppedId: UUID, edge: DropEdge?) {
        guard case .leaf(let id, _) = layout else { return }
        switch edge {
        case .left:
            layout.splitLeaf(id: id, axis: .horizontal, newServiceId: droppedId, newSide: .first)
        case .right:
            layout.splitLeaf(id: id, axis: .horizontal, newServiceId: droppedId, newSide: .second)
        case .top:
            layout.splitLeaf(id: id, axis: .vertical, newServiceId: droppedId, newSide: .first)
        case .bottom:
            layout.splitLeaf(id: id, axis: .vertical, newServiceId: droppedId, newSide: .second)
        case nil:
            layout.replaceService(panelId: id, newServiceId: droppedId)
        }
    }
}

private enum DropEdge: Equatable {
    case left, right, top, bottom
}
```

- [ ] **Step 4.2: Build — verify it compiles**

In Xcode: Product → Build (⌘B). Expected: Build Succeeded.
Note: `LogPanelView` will need a `panelId` param — that comes in Task 5. If you get a compile error on `LogPanelView(panelId:...)`, temporarily keep the old signature and add a `panelId` param with a default in Task 5.

- [ ] **Step 4.3: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/PanelLayoutView.swift
git commit -m "feat: add PanelLayoutView with recursive rendering and edge drop zones"
```

---

## Task 5: LogPanelView — add panelId + bookmark toolbar + bookmark rendering

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift`

- [ ] **Step 5.1: Add panelId parameter**

In `LogPanelView`, the struct currently starts with:
```swift
struct LogPanelView: View {
    @EnvironmentObject var core: AppCore
    let serviceId: UUID?
    var project: Project?
```

Replace with:
```swift
struct LogPanelView: View {
    @EnvironmentObject var core: AppCore
    let panelId: UUID
    let serviceId: UUID?
    var project: Project?
```

- [ ] **Step 5.2: Add bookmark computed property**

After the existing `private var activeProject` computed property, add:

```swift
    private var bookmark: LogBookmark? {
        core.bookmarks[panelId]
    }
```

- [ ] **Step 5.3: Add bookmark toolbar controls to toolbar**

In the `toolbar` computed property, the current `HStack` ends with:
```swift
            if !chips.isEmpty {
                saveAsRuleButton
            }
        }
```

Replace that closing `}` of the HStack with:
```swift
            if !chips.isEmpty {
                saveAsRuleButton
            }
            Divider().frame(height: 14)
            bookmarkControl
        }
```

Then add the `bookmarkControl` view after the `saveAsRuleButton` property:

```swift
    private var bookmarkControl: some View {
        Group {
            if let bm = bookmark, bm.isCompleted {
                // 已完成：复制、导出、清除
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
            } else if let bm = bookmark, bm.isActive {
                // 活跃中：结束按钮 + 计数 badge
                HStack(spacing: 4) {
                    Text("\(bm.lockedLogs.count)")
                        .font(.system(size: 9, weight: .bold))
                        .padding(.horizontal, 5)
                        .padding(.vertical, 2)
                        .background(Color.red.opacity(0.15))
                        .cornerRadius(4)
                        .foregroundColor(.red)
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
                }
            } else {
                // 无书签：开始按钮
                Button {
                    core.startBookmark(panelId: panelId)
                } label: {
                    Image(systemName: "record.circle")
                        .font(.system(size: 14))
                        .foregroundColor(.green)
                }
                .buttonStyle(.plain)
                .help("开始书签标记 (⌘⇧B)")
                .keyboardShortcut("b", modifiers: [.command, .shift])
            }
        }
    }

    private func exportBookmark(_ bm: LogBookmark) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "superdev-log-\(Int(bm.startTime?.timeIntervalSince1970 ?? 0)).log"
        panel.allowedContentTypes = [.plainText]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            try? bm.formattedText.write(to: url, atomically: true, encoding: .utf8)
        }
    }
```

- [ ] **Step 5.4: Extend makeLogDisplay() to inject bookmark marker rows**

`LogDisplay` currently holds `logs: [LogEntry]`. We need to add bookmark marker rendering. Since marker rows are visual-only, we'll use a wrapper enum for display items.

**5.4a** — Add `LogDisplayItem` enum and update `LogDisplay` struct. Find and replace the inner struct:

Replace:
```swift
    private struct LogDisplay {
        let logs: [LogEntry]
        let stats: (total: Int, folded: Int, errors: Int, warns: Int)
    }
```

With:
```swift
    private enum LogDisplayItem: Identifiable {
        case entry(LogEntry)
        case markerStart(id: UUID, date: Date)
        case markerEnd(id: UUID, date: Date)

        var id: UUID {
            switch self {
            case .entry(let e): return e.id
            case .markerStart(let id, _): return id
            case .markerEnd(let id, _):   return id
            }
        }
    }

    private struct LogDisplay {
        let items: [LogDisplayItem]
        let stats: (total: Int, folded: Int, errors: Int, warns: Int)
    }
```

**5.4b** — Update `makeLogDisplay()`. Replace the existing implementation:

```swift
    private func makeLogDisplay() -> LogDisplay {
        let logs = core.filteredLogs(
            serviceId: serviceId,
            includeChips: includeChips,
            excludeChips: excludeChips,
            chipLogic: chipLogic.logFilterLogic
        )
        var folded = 0
        var errors = 0
        var warns = 0
        for entry in logs {
            if entry.repeatCount > 1 { folded += entry.repeatCount - 1 }
            if entry.level == .error { errors += 1 }
            else if entry.level == .warn { warns += 1 }
        }
        return LogDisplay(logs: logs, stats: (logs.count, folded, errors, warns))
    }
```

With:
```swift
    private func makeLogDisplay() -> LogDisplay {
        let logs = core.filteredLogs(
            serviceId: serviceId,
            includeChips: includeChips,
            excludeChips: excludeChips,
            chipLogic: chipLogic.logFilterLogic
        )

        var items: [LogDisplayItem] = []
        var folded = 0
        var errors = 0
        var warns = 0

        if let bm = bookmark, let startTime = bm.startTime {
            if bm.isCompleted, let endTime = bm.endTime {
                // 已完成：显示锁定日志，用标记行包裹
                let outside = logs.filter { $0.timestamp < startTime || $0.timestamp > endTime }
                let before = outside.filter { $0.timestamp < startTime }
                let after  = outside.filter { $0.timestamp > endTime }
                items += before.map { .entry($0) }
                items.append(.markerStart(id: UUID(), date: startTime))
                items += bm.lockedLogs.map { .entry($0) }
                items.append(.markerEnd(id: UUID(), date: endTime))
                items += after.map { .entry($0) }
            } else {
                // 活跃中：正常日志流，活跃起点后插入开始标记行
                let before = logs.filter { $0.timestamp < startTime }
                let after  = logs.filter { $0.timestamp >= startTime }
                items += before.map { .entry($0) }
                if !after.isEmpty || bm.isActive {
                    items.append(.markerStart(id: UUID(), date: startTime))
                }
                items += after.map { .entry($0) }
            }
        } else {
            items = logs.map { .entry($0) }
        }

        for item in items {
            if case .entry(let e) = item {
                if e.repeatCount > 1 { folded += e.repeatCount - 1 }
                if e.level == .error { errors += 1 }
                else if e.level == .warn { warns += 1 }
            }
        }

        return LogDisplay(items: items, stats: (items.filter { if case .entry = $0 { true } else { false } }.count, folded, errors, warns))
    }
```

**5.4c** — Update `logList(logs:)` call site in `body` to use `items`:

Replace:
```swift
            logList(logs: display.logs)
```

With:
```swift
            logList(items: display.items)
```

**5.4d** — Update `logList` signature and `ForEach`:

Replace:
```swift
    private func logList(logs: [LogEntry]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(logs) { entry in
                        logRow(entry)
                            .id(entry.id)
                    }
                }
```

With:
```swift
    private func logList(items: [LogDisplayItem]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(items) { item in
                        switch item {
                        case .entry(let entry):
                            logRow(entry, isBookmarked: isInActiveBookmark(entry))
                                .id(item.id)
                        case .markerStart(_, let date):
                            bookmarkMarkerRow(isStart: true, date: date)
                                .id(item.id)
                        case .markerEnd(_, let date):
                            bookmarkMarkerRow(isStart: false, date: date)
                                .id(item.id)
                        }
                    }
                }
```

**5.4e** — Fix scroll-to-bottom references: `logs.last` → `items.last`, `logs.count` → `items.count`. In the same `logList` body, replace two occurrences of:
```swift
                    if let last = logs.last {
```
with:
```swift
                    if let last = items.last {
```
and:
```swift
                    newLogCount += max(0, newCount - oldCount)
```
stays the same. Also fix the `onChange(of: logs.count)`:
```swift
            .onChange(of: logs.count) { oldCount, newCount in
```
→
```swift
            .onChange(of: items.count) { oldCount, newCount in
```

**5.4f** — Update `logRow` signature to accept `isBookmarked`:

Replace:
```swift
    private func logRow(_ entry: LogEntry) -> some View {
```
With:
```swift
    private func logRow(_ entry: LogEntry, isBookmarked: Bool = false) -> some View {
```

And update `.background(rowBackground(entry.level))` to:
```swift
            .background(isBookmarked ? bookmarkRowBackground(entry.level) : rowBackground(entry.level))
```

**5.4g** — Add the new helper views/functions after `rowBackground`:

```swift
    private func isInActiveBookmark(_ entry: LogEntry) -> Bool {
        guard let bm = bookmark else { return false }
        if bm.isActive, let start = bm.startTime {
            return entry.timestamp >= start
        }
        if bm.isCompleted, let start = bm.startTime, let end = bm.endTime {
            return entry.timestamp >= start && entry.timestamp <= end
        }
        return false
    }

    private func bookmarkRowBackground(_ level: LogLevel) -> Color {
        // 深琥珀色，比正常行背景稍深，与书签区域主题一致
        switch level {
        case .error: return Color.red.opacity(0.18)
        case .warn:  return Color.yellow.opacity(0.12)
        default:     return Color(red: 0.12, green: 0.10, blue: 0.04)
        }
    }

    private func bookmarkMarkerRow(isStart: Bool, date: Date) -> some View {
        let label = isStart ? "▶ 开始标记" : "■ 结束标记"
        let timeStr = date.formatted(.dateTime.hour().minute().second())
        return HStack {
            Spacer()
            Text("\(label)  \(timeStr)")
                .font(.system(size: 10, weight: .bold))
                .foregroundColor(.white)
            Spacer()
        }
        .padding(.vertical, 4)
        .background(Color(red: 0.47, green: 0.22, blue: 0.04))
    }
```

- [ ] **Step 5.5: Fix callers of LogPanelView**

`MainWindowView.swift` currently passes `serviceId:` and `project:`. After Task 6 it will be replaced entirely, but to keep the build green now, temporarily update the call in `MainWindowView.swift`:

Replace:
```swift
            LogPanelView(
                serviceId: selectedServiceId,
                project: selectedProject
            )
```
With:
```swift
            LogPanelView(
                panelId: UUID(),
                serviceId: selectedServiceId,
                project: selectedProject
            )
```

- [ ] **Step 5.6: Build — verify it compiles**

In Xcode: Product → Build (⌘B). Expected: Build Succeeded.

- [ ] **Step 5.7: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/LogPanelView.swift SuperDev/SuperDev/UI/MainWindow/MainWindowView.swift
git commit -m "feat: add panelId, bookmark toolbar and rendering to LogPanelView"
```

---

## Task 6: Wire up MainWindowView with PanelLayout + persistence

**Files:**
- Modify: `SuperDev/SuperDev/UI/MainWindow/MainWindowView.swift`

- [ ] **Step 6.1: Replace MainWindowView body with PanelLayout state**

Replace the entire `MainWindowView.swift` content with:

```swift
// MainWindowView 持有面板布局状态并管理持久化。
//
// 职责：
//   - 维护 PanelLayout 状态树
//   - 在布局变更时写入 UserDefaults
//   - 渲染 SidebarView + PanelLayoutView
//
// 边界：
//   - 不持有服务选择状态（已移入各叶子面板）
//   - 不直接渲染 LogPanelView（由 PanelLayoutView 负责）
import SwiftUI

struct MainWindowView: View {
    @EnvironmentObject var core: AppCore
    @State private var layout: PanelLayout = Self.loadLayout()

    var body: some View {
        NavigationSplitView {
            SidebarView()
        } detail: {
            PanelLayoutView(layout: $layout)
                .onChange(of: layout) { _, newLayout in
                    Self.saveLayout(newLayout)
                    pruneOrphanBookmarks(layout: newLayout)
                }
        }
        .navigationTitle("SuperDev")
        .frame(minWidth: 800, minHeight: 500)
    }

    // MARK: - Persistence

    private static let layoutKey = "superdev.panel_layout"

    private static func loadLayout() -> PanelLayout {
        guard let data = UserDefaults.standard.data(forKey: layoutKey),
              let decoded = try? JSONDecoder().decode(PanelLayout.self, from: data) else {
            return .leaf(id: UUID(), serviceId: nil)
        }
        return decoded
    }

    private static func saveLayout(_ layout: PanelLayout) {
        if let data = try? JSONEncoder().encode(layout) {
            UserDefaults.standard.set(data, forKey: layoutKey)
        }
    }

    // 当面板被关闭时，清理对应的孤立书签
    private func pruneOrphanBookmarks(layout: PanelLayout) {
        let activeIds = Set(layout.allLeafIds)
        for panelId in core.bookmarks.keys where !activeIds.contains(panelId) {
            core.clearBookmark(panelId: panelId)
        }
    }
}
```

Also update `SidebarView` — it no longer needs the `selectedServiceId` binding. Replace the struct signature and the `List` in `SidebarView.swift`:

Replace:
```swift
struct SidebarView: View {
    @EnvironmentObject var core: AppCore
    @Binding var selectedServiceId: UUID?

    var body: some View {
        List(selection: $selectedServiceId) {
```

With:
```swift
struct SidebarView: View {
    @EnvironmentObject var core: AppCore

    var body: some View {
        List {
```

And remove `.tag(Optional(service.id))` from the service row (it's no longer needed for selection):

Replace:
```swift
                        .draggable(service.id.uuidString)
                        .tag(Optional(service.id))
```
With:
```swift
                        .draggable(service.id.uuidString)
```

- [ ] **Step 6.2: Build — verify it compiles**

In Xcode: Product → Build (⌘B). Expected: Build Succeeded.

- [ ] **Step 6.3: Commit**

```bash
git add SuperDev/SuperDev/UI/MainWindow/MainWindowView.swift SuperDev/SuperDev/UI/MainWindow/SidebarView.swift
git commit -m "feat: wire MainWindowView with PanelLayout state and persistence"
```

---

## Task 7: Manual smoke test

- [ ] **Step 7.1: Run the app and test split view**

1. Build and run in Xcode (⌘R)
2. In the sidebar, drag a service onto the **right edge** of the log panel — a new panel should appear to the right showing that service's logs
3. Drag another service onto the **bottom edge** of either panel — a vertical split should appear
4. Click `×` on any panel — it should close and the sibling should expand
5. Drag a service to the **center** of a panel — the service should switch without splitting
6. Quit and relaunch — the layout should be restored

- [ ] **Step 7.2: Test bookmarks**

1. In any panel, click the green `⏺` button — it turns red with a counter
2. Wait for a few log lines to arrive (or start a service)
3. Click the red `⏹` button — the button state changes to show copy/export/clear
4. Verify the bookmarked log rows have amber background between the two marker rows
5. Click the copy button — paste into a text editor to verify format
6. Click the export button — save and verify the `.log` file content
7. Click `✕ 清除` — marker rows and amber background disappear
8. Test `⌘⇧B` shortcut for start and end

- [ ] **Step 7.3: Run full test suite**

```
xcodebuild test -project SuperDev/SuperDev.xcodeproj -scheme SuperDev -destination 'platform=macOS' 2>&1 | grep -E "PASS|FAIL|error:"
```
Expected: All tests PASS, no errors.

- [ ] **Step 7.4: Final commit**

```bash
git add -A
git commit -m "chore: smoke test verified, split view and bookmarks complete"
```
