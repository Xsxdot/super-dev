# 日志面板大片空白：已确认根因与修复建议

> 日期：2026-07-11<br>
> 状态：根因已通过真实运行打点与最小红绿实验确认<br>
> 范围：只记录诊断结论与修复建议，本次未修改产品代码

## 1. 现象

切换到某个运行态日志 tab 后，日志面板会偶发出现以下现象：

- 面板中大部分区域为空白；
- 滚动条显示当前仍处于一个很长的可滚动区间；
- 需要向上滚动很久才能回到日志内容；
- 底部状态栏仍显示“200 条”，日志数据并未丢失。

用户截图时间为 2026-07-11 18:38:46（Asia/Shanghai）。前端诊断日志使用 UTC，对应的故障时窗为 10:38:27Z 左右。

## 2. 结论

这不是日志数据丢失，也不是普通 CSS 留白。

已确认的最上游根因是：

> workspace tab 切换时，Vue 复用了上一个 tab 的 `PanelLeaf` / `LogPanel` 组件实例。`LogPanel` 内部的 `cachedDisplay`、TanStack virtualizer 测量缓存和滚动偏移因此跨 tab 串扰。

`LogPanel` 现有的两帧“贴底沉降”判定又过早结束，使旧滚动状态被长时间保留，最终显示为大片空白。

因果关系应区分为：

- **根因/触发器**：组件身份未按 workspace leaf/source 隔离；
- **放大器**：虚拟列表的贴底逻辑只看连续两帧 `totalSize` 相等，没有等待新组件尺寸和 ResizeObserver 真正沉降。

## 3. 真实运行证据

桌面端的 `superdev:log-panel` 打点会上报到内部虚拟 deployment `__desktop__`。截图前的时序如下：

| UTC 时间 | 事件 | 关键数据 | 含义 |
| --- | --- | --- | --- |
| 10:38:27.102 | workspace/track 切换 | 新 `panelId=d22bfc1f-...` | 切到截图对应的日志 tab |
| 10:38:27.107 | `virtualizer.count_change` | `oldCount=471, newCount=0` | **新 panel 继承了上一个 tab 的 471 条本地显示状态** |
| 10:38:27.590 | `filter.incremental` | `rawCount=200, visibleCount=200` | 新数据正常加载，没有被过滤丢失 |
| 10:38:27.598 | `virtualizer.count_change` | `newCount=200, rangeStart=201` | 202 个 display item 中，虚拟视窗已落到最后一项附近，状态明显带有旧滚动上下文 |
| 10:38:27.602 | `scroll.settle_bottom.done` | `frames=2, itemCount=202, totalSize=4915` | 仅 4ms 后就宣布贴底沉降完成 |
| 10:38:28.326 | `scroll_intent.transition` | `follow-bottom -> idle, reason=wheel-up` | 用户已开始向上滚动寻找日志 |

最关键的是 `oldCount=471`：该数值正是切换前一个 tab 的显示数量。如果新 tab 挂载了新的 `LogPanel` 实例，其 `cachedDisplay` 初始值应为 0，不可能出现 471。

## 4. 代码链路

### 4.1 workspace tab 切换只替换布局树

`desktop/src/components/Workspace/WorkspaceShell.vue:64-67`：

```vue
<template v-else-if="isRuntimeTab">
  <RuntimeWorkbenchHeader />
  <PanelLayout />
</template>
```

不同运行态 tab 共用同一个 `PanelLayout` 渲染位置。

`desktop/src/stores/workspace.ts:219-226`：

```ts
function activateTab(tabId: string) {
  saveActiveLogWorkspaceLayout()
  activeTabId.value = tabId
  const tab = activeTab.value
  if (isLogWorkspaceTab(tab)) {
    usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
  }
}
```

切换时只把 `panelStore.root` 换成目标 tab 的布局树。

### 4.2 `PanelLeaf` 缺少组件 key

`desktop/src/components/Panel/PanelLayout.vue:52-64`：

```vue
<div v-if="node.type === 'leaf'" class="panel-leaf-wrapper">
  <PanelLeaf
    :panel-id="node.id"
    :source="node.source"
    ...
  />
</div>
```

这里没有 `:key`。当两个 tab 的根节点都是 leaf 时，Vue 看到的仍是“同一个位置、同一种组件”，因此只更新 props，不会销毁并重建 `PanelLeaf`。

`PanelLeaf` 内的 `LogPanel` 也随之被复用，保留了：

- `cachedDisplay`；
- `scrollIntent`；
- `useVirtualizer(...)` 实例；
- virtualizer 的 item size cache、range 和 scroll offset；
- 正在进行的贴底/历史锚定状态。

### 4.3 贴底逻辑过早判定稳定

`desktop/src/components/Panel/LogPanel.vue:608-649`：

```ts
if (c > 0) virtualizer.value.scrollToIndex(c - 1, { align: 'end' })
const totalSize = virtualizer.value.getTotalSize()
frame++
if (totalSize === lastTotalSize || frame >= SETTLE_TO_BOTTOM_MAX_FRAMES) {
  return
}
```

当组件刚切换、DOM 尺寸和 ResizeObserver 尚未完成时，两个相邻 rAF 帧中的 `totalSize` 可以暂时不变。当前代码会把这个“暂时安静”误认为“测量已沉降”。

旧的 scroll offset/range 因此没有被完整校正，就会在新列表中形成巨大的可滚动空白区。

## 5. 最小实验

### 5.1 组件身份实验（根因实验）

临时测试挂载一棵 `leaf-a` 布局树，记录 `PanelLeaf` 实例 ID，再将 `panelStore.root` 替换为 `leaf-b`。

当前代码结果：

```text
expected '1' not to be '1'
```

表明 props 已变成 `leaf-b`，但组件实例仍是原来的第 1 个实例。

只做一个变量修改：

```vue
<PanelLeaf :key="node.id" ... />
```

同一测试立即通过，证明缺失的组件 key 是 tab 状态串扰的直接原因。

### 5.2 延迟测量实验（放大器实验）

临时测试让 `getTotalSize()` 前两次返回 66，第三次才返回真实高度 300。

当前 `scrollToBottom()` 只读取两次高度就退出：

```text
expected 2 to be greater than or equal to 3
```

这证明只要真实测量晚于一个“假稳定帧”，当前贴底逻辑就会提前结束。

上述两个临时测试和临时代码改动在诊断结束后均已撤销。

## 6. 已排除的方向

### 6.1 日志数据未加载

已排除。真实打点明确显示：

```text
rawCount=200
visibleCount=200
```

日志已加载，且 200 条全部通过过滤。

### 6.2 过滤器计数与 virtualizer count 不一致

已排除为本次的首要原因。运行时 `rawCount` / `visibleCount` / `newCount` 在新页面上均为 200。

### 6.3 后端日志分页或日志丢失

已排除。`history.initial.loaded` 明确返回 `added=200`，问题发生在前端渲染与滚动状态层。

### 6.4 2026-07-03 修复的历史回填抢滚动问题

这是相邻但不同的故障：

- 上次根因：程序化 scroll 被误当成用户上滚，触发历史回填；
- 本次根因：tab 切换时复用了旧 `LogPanel/virtualizer` 实例。

本次时序中已出现 `history.prefetch.skip_follow_bottom`，说明上次的防护在生效；但它无法阻止组件实例跨 tab 复用。

## 7. 建议修复顺序

### P0：隔离 workspace leaf / source 的组件身份

先修正最上游的身份问题，避免继续在滚动代码上叠加补丁。

已验证的最小改动是：

```vue
<PanelLeaf
  :key="node.id"
  :panel-id="node.id"
  ...
/>
```

这可以确保两个不同 workspace leaf 不共享 `LogPanel` 本地状态。

还需要考虑“同一 leaf 中心替换日志来源”的情况。更完整的隔离方式是让 `LogPanel` 的 key 同时包含 `panelId` 和 deployment/source 身份，或在 source 变更时显式重建 virtualizer。

### P1：增加组件身份回归测试

回归测试至少覆盖：

1. 从 `leaf-a/dep-a` 切到 `leaf-b/dep-b` 后，`PanelLeaf` 必须是新实例；
2. 新 `LogPanel` 的初始 display count 必须从 0 开始，不得携带前一来源的 count；
3. 从长日志列表切到短日志列表后，不得保留旧 scroll offset / virtual range。

### P2：再加固贴底沉降逻辑

在组件身份隔离后，再使用真实 virtualizer 测试评估：

- 是否仍需要手写多帧 `scrollToBottom()`；
- 是否改用 TanStack Virtual 3.16 的 `anchorTo: 'end'` / `followOnAppend`；
- 若保留手写 settle，应要求多个稳定帧，并同时校验可视 range、viewport 尺寸和距底部距离，不能只比较 `totalSize`。

这一层是额外加固，不应取代 P0 的组件身份修复。

## 8. 验收标准

修复完成后需同时满足：

1. 在两个日志数量和行高差异较大的 runtime tab 间往返切换，不再出现大片空白；
2. 新面板首次 `virtualizer.count_change` 不得带有上一个 tab 的 `oldCount`；
3. follow-bottom 下 `scrollHeight - scrollTop - clientHeight` 稳定接近 0；
4. 向上翻历史时锚点不漂移；
5. 离开底部后，实时日志不抢回视口；
6. 点击“跳到底部”可稳定回到最新日志；
7. 证据钉、时间对齐、过滤重建与历史回填回归通过；
8. 至少运行聚焦的 PanelLayout/LogPanel 测试、全量 desktop Vitest、`vue-tsc` 和 Vite build。

## 9. 与现有评审稿的关系

`docs/agents/logpanel-blank-fix-plan.md` 将“手写 settle 与 TanStack Virtual 3.16 内建 reconcile 的交互”定义为根因。该分析对本文 P2 仍有价值，但它没有解释真实时序中为什么新 panel 会继承上一 tab 的 `oldCount=471`。

根据本次新证据，应调整修复顺序：

1. 先修组件身份隔离；
2. 用真实 tab 切换场景验收；
3. 若仍有贴底偏移，再收敛手写 settle 与 TanStack 内建机制。

不建议在未修复组件身份前，直接按旧评审稿删除/替换全部贴底逻辑；那会将组件跨 tab 串扰的事实继续留在架构中。
