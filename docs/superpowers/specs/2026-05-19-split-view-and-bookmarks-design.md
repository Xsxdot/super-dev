# Split View & Log Bookmarks Design

Date: 2026-05-19

## Overview

Two new features for SuperDev's log panel:

1. **Split View** — drag a service from the sidebar onto a panel edge to split the view and show multiple services' logs side by side
2. **Log Bookmarks** — mark a start/end time range in any panel; logs in that range are locked in memory and can be copied or exported

---

## Feature 1: Split View

### Data Model

A recursive `PanelLayout` enum replaces the single `serviceId` in `MainWindowView`. It lives in `MainWindowView` as `@State` and is persisted to `UserDefaults` on change.

```swift
enum PanelLayout: Codable, Identifiable {
    case leaf(id: UUID, serviceId: UUID?)
    case split(id: UUID, axis: Axis, ratio: CGFloat, first: PanelLayout, second: PanelLayout)
}
```

- `axis` — `.horizontal` (left/right) or `.vertical` (top/bottom)
- `ratio` — split ratio, default 0.5, adjustable by dragging the divider
- Initial state: single `.leaf` with the first available serviceId

### Rendering

A new `PanelLayoutView` recursively renders the tree:

- **leaf node** → `LogPanelView` wrapped in a `ZStack` with a transparent drag-drop overlay
- **split node** → `HSplitView` or `VSplitView` (based on axis), recursively rendering `first` and `second` children

### Drag-and-Drop Interaction

**Source:** Sidebar service rows are `.draggable(service.id)` (drag payload: `UUID`).

**Target:** Each `leaf` panel is divided into 5 drop zones:
- Left/right edges (20% width each) → horizontal split, new panel on that side
- Top/bottom edges (20% height each) → vertical split, new panel on that side
- Center (remaining area) → replace the current panel's service without splitting

On hover over an edge zone, a highlight overlay appears indicating the split direction. On drop:
1. Locate the `leaf` node in the layout tree by its `id`
2. Replace it with a `.split` node: one child is the original leaf (unchanged), the other is a new `.leaf` with the dropped `serviceId`
3. Axis and position determined by the drop zone

### Panel Header

Each leaf panel gains a compact header bar showing:
- The bound service name (or "未选择" if nil)
- A `×` close button

**Close behavior:** Remove the leaf from the tree; its sibling node is promoted to replace the parent split node.

### Persistence

`PanelLayout` conforms to `Codable`. On every layout change, encode and save to `UserDefaults` under key `superdev.panel_layout`. On app launch, decode and restore; fall back to a single leaf if missing or corrupt.

---

## Feature 2: Log Bookmarks

### Data Model

```swift
struct LogBookmark {
    let panelId: UUID
    var startTime: Date?
    var endTime: Date?
    var lockedLogs: [LogEntry]  // retained independently of the main log buffer
}
```

`AppCore` owns:

```swift
@Published var bookmarks: [UUID: LogBookmark] = [:]
```

Key is `panelId` (the leaf node's `id`).

### Lifecycle

**Start (toolbar button or `⌘⇧B`):**
- Create or reset `LogBookmark` for this panelId
- Set `startTime = Date()`, clear `lockedLogs`
- Begin appending new incoming logs for this panel to `lockedLogs` in real time

**End (toolbar button or `⌘⇧B` again):**
- Set `endTime = Date()`
- Stop appending to `lockedLogs`

**Clear (`✕` button):**
- Remove the bookmark entirely; `lockedLogs` is freed

### Toolbar UI

The bookmark control sits at the right end of `LogPanelView`'s toolbar, cycling through three states:

| State | Control shown | Shortcut |
|-------|--------------|----------|
| No bookmark | `▶ 开始` (green) | `⌘⇧B` |
| Started, no end | `■ 结束` (red) + locked-count badge | `⌘⇧B` |
| Completed | Copy button + Export button + `✕ 清除` | — |

### Log List Rendering

`makeLogDisplay()` is extended to check the active bookmark:

- **Started, no end:** Append a synthetic marker row `"▶ 开始标记 HH:mm:ss"` before the first locked log. Rows at or after `startTime` render with a deep amber background (`Color(red:0.12, green:0.10, blue:0.04)`).
- **Completed:** Display `lockedLogs` directly for the bookmarked range (bypassing the normal filter), wrapped between a start marker row and an end marker row. Logs outside the range render normally.

Marker rows are visual-only synthetic entries — they are not `LogEntry` objects and are not included in copy/export output.

### Lock Mechanism

`lockedLogs` is stored inside `LogBookmark` in `AppCore`. It is **independent of the main `logs` array** and is never subject to buffer eviction. It persists until the user explicitly clears the bookmark.

New logs are appended to `lockedLogs` on the same path that appends to the main `logs` array, gated by `bookmark.endTime == nil`.

### Export / Copy

**Copy:** Format each entry in `lockedLogs` as `"HH:mm:ss [service] LEVEL message"`, join with newlines, write to `NSPasteboard`.

**Export:** Present `NSSavePanel` with default filename `superdev-log-<startTime>.log`, write the same formatted text to the chosen path.

---

## Files Affected

| File | Change |
|------|--------|
| `AppCore.swift` | Add `bookmarks` published dict; add bookmark start/end/clear methods; pipe new logs to active bookmarks |
| `MainWindowView.swift` | Replace `selectedServiceId` + single `LogPanelView` with `PanelLayout` state + `PanelLayoutView` |
| `LogPanelView.swift` | Add bookmark toolbar controls; extend `makeLogDisplay()` for bookmark rendering; add panel `id` param |
| `SidebarView.swift` | Add `.draggable(service.id)` to service rows |
| `PanelLayoutView.swift` | New file — recursive split renderer + drop target logic |

---

## Out of Scope

- Saving/restoring bookmark content across app restarts (in-memory only)
- More than one active bookmark per panel at a time
- Bookmark naming or labeling
