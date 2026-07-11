# 日志面板底部空白 — 根因与修复方案（评审稿）

> 状态：待评审 · 只出方案不改代码
> 症状：LogPanel 实时追踪（follow-bottom）中，日志停在某时间点，下方出现大片空白，往上滑很久才见日志。测试全绿但真机反复出现。

---

## 1. 根因（已通过精读 `@tanstack/virtual-core@3.16.0` 源码钉死）

虚拟列表依赖 `@tanstack/vue-virtual@3.13.26`，底层 `virtual-core` 实际解析到 **3.16.0**。这个版本相对 3.13 引入了一套全新的**滚动沉降（scroll-reconcile）+ 边测量边滚动（measure-during-scroll）+ 端点锚定（anchorTo）** 机制。而 `LogPanel.vue` 里手写的 `scrollToBottom` 多帧 rAF 沉降循环，是照旧版 API 心智模型写的补丁，**两者在抢滚动方向盘**，最终露出空白。

### 空白产生链条

| # | 环节 | 代码位置 | 问题 |
|---|------|----------|------|
| 1 | 行高先按估算 | `estimateSize: () => 22`（LogPanel.vue:954） | `scrollToIndex` 用 22px 估算算 offset 滚一次 |
| 2 | 底部行真实高度更大 | 多行日志常 40–60px | `measureElement` 该上报真高、撑大 `getTotalSize()` |
| 3 | **框架不补偿贴底** | 3.16 默认 `anchorTo:"start"`（core:256）；`resizeItem` 的 `wasAtEnd` 分支要求 `anchorTo==="end"`（core:798） | 当前配置下自动贴底补偿**永远不触发** |
| 4 | **两个 rAF 循环互抢** | 手写 settle 每帧 `scrollToIndex(c-1,{align:'end'})`（LogPanel.vue:629）；3.16 的 `scrollToIndex` 自设 `scrollState` + 启 `reconcileScroll`（core:960） | 手写循环与框架 reconcile 并行，互相拉扯 |
| 5 | **真实测量被推迟** | `shouldMeasureDuringScroll`：measureElement 仅在 `(!isScrolling\|\|scrollState)` 时写实测高度（core:773） | 手写 settle 不断刷 `scrollState/isScrolling` → 底部行真高测量被反复跳过 |
| 6 | **settle 提前退出** | 终止条件 `totalSize === lastTotalSize`（LogPanel.vue:633） | 底部行还是 22px 估算时「连续两帧相等」提前满足，循环退出 |
| 7 | **视口永久停在旧位** | settle 退出后 `scrollState` 已清（core:1100）、`anchorTo:"start"` 不补偿 | 真实高度姗姗来迟、`getTotalSize` 变大，但滚动位置没跟上 → 底部差额全成空白 |

### 为什么测试全绿

`LogPanel.test.ts` 全程用 `virtualizerMock` 把真 virtualizer mock 掉，`getTotalSize` 直接返回固定值（如 22）。**真实的 measure / totalSize 时序永远进不了测试** —— 这是测试盲区，也是这个 bug 反复被"修好"又复发的原因。

### 为什么这是架构问题而非又一个补丁点

历次提交 `4cd1c0d stabilize log bottom scroll`、`539cc42 programmaticScroll timing`、`8860834 avoid measure while scrolling`、`869a668/eb079de measure 时机` 全在**手写 settle 逻辑**上打补丁。根因是「手写贴底 vs 新版框架内建贴底」的机制冲突 —— 满足 systematic-debugging 的 **Phase 4.5：3+ 次修复失败 = 质疑架构**。继续在 settle 上打补丁不会根治。

---

## 2. 修复方向：交给框架内建机制单一负责贴底

tanstack virtual 3.16 恰好内建了一套**专为"实时日志贴底追踪"设计的官方 API**（字段名、类型已在源码 `.d.ts` 核对）：

```ts
type ScrollAnchor  = 'start' | 'end'
type FollowOnAppend = boolean | 'auto' | 'smooth' | 'instant'

anchorTo?: ScrollAnchor          // 'end' → count 变化时锚定底部
followOnAppend?: FollowOnAppend  // 'auto' → 新行追加且当前贴底时自动跟随到底
scrollEndThreshold?: number      // 判定"是否已贴底"的像素阈值
```

Vue 适配层 `useVirtualizer` 用 `...unref(options)` 原样透传所有 option（vue-virtual 适配器 46 行已确认），这三个字段直接可用。

**核心思路**：删掉手写 `scrollToBottom` 多帧沉降循环，改由框架内建 reconcile 单一负责贴底。前端只保留"是否处于 follow 意图"的裁决（现有 `ScrollIntentMachine` 不动）。

---

## 3. 涉及的改动点（逐条，供评审）

### 3.1 virtualizer 配置（LogPanel.vue:950-958）
```diff
 const virtualizer = useVirtualizer(
   computed(() => ({
     count: displayItems.value.length,
     getScrollElement: () => logListEl.value,
     estimateSize: () => 22,
     getItemKey: (index) => displayItems.value[index]?.id ?? index,
     overscan: LOG_VIRTUAL_OVERSCAN,
+    anchorTo: 'end',
+    followOnAppend: 'auto',
+    // scrollEndThreshold 默认 1；日志行较高，建议放宽到约一行高度，避免"差几像素不算贴底"
+    scrollEndThreshold: 24,
   }))
 )
```

### 3.2 删除手写贴底 settle 循环
- 删 `scrollToBottom`（614-649）整段多帧 rAF 沉降逻辑，及其配套的 `settleToBottomToken`（101）、`SETTLE_TO_BOTTOM_MAX_FRAMES`（106）。
- `scrollMachine` 的 `scrollToBottom` 回调（561）改为**单次** `virtualizer.value.scrollToIndex(count-1,{align:'end'})`，或直接调框架的 `scrollToIndex`/`scrollToOffset`，把"沉降"交给框架 reconcile。
- 首屏/切 tab 的 `scrollToBottom()`（202）、`subscribeDeployment` 里的 `jumpToBottom`（193）同样收敛到单次滚动。

### 3.3 复核 `commitDisplay` 的主动 measure（576-606）
- 现有逻辑：过滤重建 或 follow 且可见行增长时手动 `measureVirtualizer()`。切到 `anchorTo:'end'` 后，**新行 append 的贴底由框架负责**，这里的手动 measure 是否还需要要重新评估——很可能可以进一步简化，但需真机验证后再删，先保留观察。

### 3.4 非贴底的 scrollToIndex 调用（不受影响，需回归确认）
`scrollToDisplayLog`（893）、`jumpToLog`（901）、`alignToTime`→`scrollToLogId`：这些是「跳到指定日志」，用 `align:'center'`，与 `anchorTo:'end'` 不冲突（anchorTo 只影响 count 变化时的锚定，不影响主动 scrollToIndex）。需在验证清单里回归证据钉/时间对齐/跳转。

### 3.5 历史回填锚点（tryLoadMoreHistory 732-784）
向上翻历史时用 `beginHistoryAnchor`/`settleHistoryAnchor` 锚定顶部日志 id。`anchorTo:'end'` 只锚底部，向上翻历史时意图是 `anchor-history`（非 follow-bottom），需确认 `followOnAppend` 不会在历史回填（往数组**头部**插入）时误触发跟随。**核对点**：followOnAppend 仅在 `nextCount > prevCount && isAtEnd && 末尾 key 变化`（core:287）时触发；历史回填改变的是头部、末尾 key 不变，理论上不误触发——但列为**必验项**。

---

## 4. 补一个能真实测出空白的回归测试（关键）

现有测试全 mock virtualizer，测不出本 bug。方案要求新增一个**不 mock virtualizer、用真实 DOM 高度**的测试：

- 用 jsdom + 真实 `Virtualizer`（不走 `virtualizerMock`），给日志行 stub 一个 >22px 的真实高度（如 48px）。
- 断言：follow-bottom 下持续 append 新行后，`scrollTop` 贴底（`scrollHeight - scrollTop - clientHeight <= scrollEndThreshold`），**底部无空白**（`getTotalSize()` 与最后一行 `end` 一致、视口停在底部）。
- 这个测试在**修复前应失败、修复后通过**，作为根因确证与回归护栏。
- ⚠️ jsdom 不做真实布局，ResizeObserver/getBoundingClientRect 需要 stub 或换 happy-dom；若纯单测难以真实测量，退化为在**真机 dev server + 浏览器**里跑一段脚本读 `getTotalSize`/`scrollTop` 作为验证证据（见 §5）。

---

## 5. 真机验证步骤（改完后执行）

1. 起 desktop dev server（`cd desktop && npm run dev`，非 SuperDev 托管项目，可常规启动），在浏览器打开。
2. 打开一个 dev 部署的运行态面板，保持"实时追踪中"。
3. 让日志持续到达一段时间（可对 SuperDev 托管的某个 dev 服务发请求造日志），观察底部是否再出现空白。
4. 在 console 里读证据：
   ```js
   const el = document.querySelector('.log-list')
   el.scrollHeight - el.scrollTop - el.clientHeight   // 应 ≈ 0（贴底，无空白）
   ```
5. 回归四件事：① 上滑翻历史→锚点不漂；② 上滑后停住→不被实时日志抢回底部；③ 点"跳到底部"→回贴底；④ 证据钉/时间对齐跳转→定位准确。
6. 跑 `cd desktop && npm run test`，确认新增回归测试通过、其余不回归。

---

## 6. 风险与回滚

- **风险**：`anchorTo:'end'` 改变了框架的锚定语义，可能与历史回填的头部插入、证据对齐的 center 跳转产生新交互。§3.5、§3.4 已列为必验项。
- **回滚**：改动集中在 LogPanel.vue 单文件 + 新增一个测试，`git revert` 即可回到手写 settle。
- **关联既有记忆**：本方案与 `project-follow-bottom-distance-mirage` / `project-log-chain-overhaul` / `project-virtual-list-measure-scroll-disturb` 描述的是**同一顽疾的不同侧面**，真根因统一到"手写 settle 与新版框架冲突"。修复落地后应回头复核那几条是否随之消解。
