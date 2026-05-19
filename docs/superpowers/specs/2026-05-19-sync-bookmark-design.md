# 同步书签（Sync Bookmark）设计文档

**日期**：2026-05-19  
**状态**：已审批

---

## 背景

用户同时开多个分栏监控不同服务时，需要逐个点击书签按钮开始/停止记录，时间戳不一致，操作繁琐。需要支持多分栏同步开始/停止书签，并支持合并复制/导出。

---

## 用户体验

### 分栏书签区域

每个 `LogPanelView` 的书签控制区新增一个"同步"勾选框，紧邻现有书签按钮左侧：

```
[☐ 同步]  [● 开始]          ← 未参与同步，独立书签，行为不变
[☑ 同步]  [● 开始]          ← 已勾选，等待同步组开始
[☑ 同步]  [■ 123 停止]      ← 同步组已开始，正在记录
[☑ 同步]  [📋 📤 ✕]         ← 同步组已完成，显示合并复制/导出
```

### 联动规则

- 勾选"同步"后加入同步组，**不立刻开始录制**
- 有 ≥1 个分栏勾选同步时，任意已勾选分栏点"开始" → 所有勾选分栏同时 `startBookmark`
- 同步录制中，任意已勾选分栏点"停止" → 所有勾选分栏同时 `endBookmark`
- 未勾选的分栏保持原有独立书签逻辑，不受同步影响
- 同步组完成后，各分栏各自显示已完成状态；复制/导出按钮触发**合并输出**

### 合并复制/导出格式（按服务分块）

```
=== 服务 A (serviceA) ===
10:01:01 [serviceA] INFO  started
10:01:02 [serviceA] ERROR timeout

=== 服务 B (serviceB) ===
10:01:01 [serviceB] INFO  connected
10:01:03 [serviceB] WARN  retry
```

- 同步组内各分栏的书签按 `serviceName` 字母序排列分块
- 导出文件名：`superdev-sync-<unix_timestamp>.log`
- 若某分栏没有绑定服务（serviceId 为 nil），跳过该分栏的合并输出

---

## 数据层设计

### AppCore 新增状态（运行时，不持久化）

```swift
/// 已勾选加入同步组的 panelId 集合
@Published var syncGroupPanelIds: Set<UUID> = []

/// 同步组是否正在录制中（true = 已 startBookmark，false = 未开始或已结束）
@Published var syncGroupIsRecording: Bool = false
```

### AppCore 新增方法

```swift
// 将 panelId 加入/移出同步组
func toggleSyncGroup(panelId: UUID)

// 开始同步录制（对所有 syncGroupPanelIds 调用 startBookmark）
func startSyncBookmark(panelIds: [UUID], serviceIds: [UUID: UUID?])

// 结束同步录制（对所有 syncGroupPanelIds 调用 endBookmark）
func endSyncBookmark()

// 返回同步组内所有已完成书签的合并格式文本（按服务分块）
func syncBookmarkFormattedText() -> String
```

### 不变的部分

- `LogBookmark` 模型结构不变
- 同步只是"批量调用现有 startBookmark / endBookmark"
- 每个分栏最多一个书签的限制不变

---

## 实现范围

| 文件 | 改动 |
|------|------|
| `AppCore/AppCore.swift` | 新增 `syncGroupPanelIds`、`syncGroupIsRecording`、4 个方法 |
| `UI/MainWindow/LogPanelView.swift` | 书签控制区新增同步勾选框，联动逻辑 |
| `AppCore/Models/LogBookmark.swift` | 不变 |

---

## 边界与约束

- 同步组状态为运行时状态，App 重启后清空，不持久化
- 合并导出逻辑在 UI 层实现，不下沉到 AppCore
- 一个分栏同一时刻只能有一个书签（现有约束不变）
- serviceId 为 nil 的分栏可以加入同步组开始/停止，但不参与合并输出
