# 日志面板虚拟滚动 + 日志清理设计

## 背景与问题

日志面板长时间运行后卡顿，根本原因有两个：

1. `seenSignatures` Set 从不清理，随日志数量无限膨胀，占用内存
2. 即使 `MAX_LOGS = 8000` 限制了数组长度，8000 个 `<LogRow>` 同时挂载在 DOM 里，滚动和重渲染都很慢

## 目标

- 长时间运行不卡顿，内存使用稳定
- 日志上限调整为 5000 条
- DOM 中始终只渲染可视窗口附近的少量行（~30–50 行）
- 保持现有功能：follow bottom、历史加载、书签标记、过滤、选择

---

## 设计

### 1. 数据层：`log.ts` 清理

**变更点：**

- `MAX_LOGS` 从 8000 → 5000
- `appendEntry` 截断时批量删除头部 500 条（而非逐条删 1 条），减少频繁 splice 开销
- 截断的同时，同步从 `seenSignatures` 中删除对应签名，防止 Set 无限膨胀

**伪代码：**

```ts
const TRIM_BATCH = 500

function appendEntry(entry: ServiceLog, log: LogEntry): boolean {
  const sig = logSignature(log)
  if (entry.seenSignatures.has(sig)) return false
  entry.seenSignatures.add(sig)
  ingest(toDisplayEntry(log), entry.logs)
  if (entry.logs.length > MAX_LOGS) {
    const removed = entry.logs.splice(0, TRIM_BATCH)
    for (const r of removed) entry.seenSignatures.delete(logSignature(r))  // 注意：需要原始 LogEntry 或 signature
  }
  // ...
}
```

> 注意：`seenSignatures` 存的是原始 `LogEntry` 的签名，截断时需要从 `DisplayLogEntry` 重建签名（字段一致，直接用 `logSignature(r)` 即可）。

**改动范围：** 仅 `stores/log.ts`，不影响 UI。

---

### 2. 虚拟滚动层：`@tanstack/vue-virtual`

**依赖：**

```
npm install @tanstack/vue-virtual
```

**核心替换：`LogPanel.vue`**

用 `useVirtualizer` 替换 `v-for displayItems` 直接渲染：

```
displayItems (全量数组，传入 virtualizer)
    ↓ useVirtualizer({ count, estimateSize, measureElement })
virtualItems (只含可视窗口 ± overscan 的条目)
    ↓ v-for
DOM 中只有 ~30–50 个节点
```

**动态行高：**

- `estimateSize: () => 22`（单行 ~22px 作为初始估算）
- 每行渲染后通过 `measureElement` 回调测量真实高度
- virtualizer 内部维护 offset 数组，滚动位置保持准确

**容器 HTML 结构：**

```html
<div ref="logListEl" class="log-list" @scroll="onScroll" @wheel="onWheel">
  <div :style="{ height: virtualizer.getTotalSize() + 'px', position: 'relative' }">
    <div
      v-for="vRow in virtualizer.getVirtualItems()"
      :key="vRow.key"
      :data-index="vRow.index"
      :ref="el => virtualizer.measureElement(el)"
      :style="{ position: 'absolute', top: vRow.start + 'px', width: '100%' }"
    >
      <!-- 根据 displayItems[vRow.index].kind 渲染对应组件 -->
    </div>
  </div>
</div>
```

---

### 3. 滚动行为整合

| 行为 | 原实现 | 新实现 |
|------|--------|--------|
| Follow bottom（跟随最新日志） | `el.scrollTop = el.scrollHeight` | `virtualizer.scrollToIndex(displayItems.length - 1, { align: 'end' })` |
| 历史加载后保持视口不跳 | 记录 `prevScrollHeight`，加载后 `scrollTop += added` | 记录加载前 `startIndex`，加载后 `virtualizer.scrollToIndex(startIndex + newItems.length, { align: 'start' })` |
| 触发加载更多历史 | `el.scrollTop < 80` | `virtualizer.range.startIndex < 5` |
| 检测是否到底（判断 isFollowing） | `scrollHeight - scrollTop - clientHeight >= 50` | 保持原判断逻辑，仍基于 scroll 事件 |

**`programmaticScroll` flag 保留**，用于区分用户滚动和代码触发的滚动，防止误触发 `isFollowing` 切换。

---

### 4. 不变部分

以下模块**不需要修改**：

- `logEngine.ts` —— ingest、normalize、fold 逻辑不变
- `logDisplay.ts` —— makeDisplayItems、computeDisplayStats 不变
- `LogRow.vue`、`BookmarkMarkerRow.vue`、`LogHistorySeparatorRow.vue` —— 纯展示组件，接口不变
- `deploymentLog.ts` —— 同步应用 seenSignatures 清理逻辑（结构相同）
- 过滤、书签、选择功能 —— 逻辑不变，仅渲染层切换到 virtual items

---

## 边界与约束

- `seenSignatures` 清理时使用 `logSignature(r)`，`DisplayLogEntry extends LogEntry`，字段兼容，签名可直接重建
- 虚拟滚动下，`data-log-id` 属性保留在 LogRow 上，`scrollToBottom` 中的 `querySelector('[data-log-id]:last-of-type')` 逻辑废弃，改用 `scrollToIndex`
- `deploymentLog.ts` 与 `log.ts` 结构相同，同步应用相同的 seenSignatures 清理策略
- 书签捕获逻辑基于 `filteredLogs` 计算属性，与虚拟滚动无关，不受影响

---

## 文件改动清单

| 文件 | 改动类型 | 说明 |
|------|---------|------|
| `stores/log.ts` | 修改 | MAX_LOGS → 5000，appendEntry 批量截断 + seenSignatures 同步清理 |
| `stores/deploymentLog.ts` | 修改 | 同步应用 seenSignatures 清理逻辑 |
| `components/Panel/LogPanel.vue` | 修改 | 引入 useVirtualizer，替换 v-for 渲染，整合滚动行为 |
| `package.json` | 修改 | 添加 `@tanstack/vue-virtual` 依赖 |
