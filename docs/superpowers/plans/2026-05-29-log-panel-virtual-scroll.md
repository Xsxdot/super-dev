# Log Panel Virtual Scroll + Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 `@tanstack/vue-virtual` 实现虚拟滚动，并清理 seenSignatures 内存泄漏，彻底解决日志面板长时间运行卡顿问题。

**Architecture:** 数据层（`log.ts` / `deploymentLog.ts`）调整日志上限为 5000 条并批量清理 `seenSignatures`；渲染层（`LogPanel.vue`）用 `useVirtualizer` 替换全量 `v-for`，DOM 中始终只挂载可视窗口附近约 30–50 行；滚动行为（follow bottom、历史加载补偿、触发加载判断）全部切换到 virtualizer API。

**Tech Stack:** Vue 3, Pinia, `@tanstack/vue-virtual`, Vitest, jsdom

---

## Task 1: 安装 @tanstack/vue-virtual

**Files:**
- Modify: `desktop/package.json`

- [ ] **Step 1: 安装依赖**

```bash
cd desktop && pnpm add @tanstack/vue-virtual
```

预期输出：`dependencies: + @tanstack/vue-virtual x.x.x` 无报错。

- [ ] **Step 2: 验证可以导入**

```bash
node -e "require('./node_modules/@tanstack/vue-virtual/dist/index.cjs')" && echo OK
```

预期输出：`OK`

- [ ] **Step 3: Commit**

```bash
git add desktop/package.json desktop/pnpm-lock.yaml
git commit -m "feat: add @tanstack/vue-virtual dependency"
```

---

## Task 2: log.ts — MAX_LOGS 调整 + seenSignatures 清理

**Files:**
- Modify: `desktop/src/stores/log.ts`
- Test: `desktop/src/stores/__tests__/log.test.ts`

- [ ] **Step 1: 写失败测试**

在 `desktop/src/stores/__tests__/log.test.ts` 末尾追加：

```typescript
describe('log trimming', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  it('超出 MAX_LOGS 时批量截断并同步清理 seenSignatures', async () => {
    vi.spyOn(agentApi, 'fetchLogs').mockResolvedValue([])
    const store = useLogStore()
    await store.subscribe('svc-trim')
    const ws = MockWebSocket.instances[0]

    // 注入 5001 条不同日志
    for (let i = 1; i <= 5001; i++) {
      ws.onmessage?.({ data: JSON.stringify(log(i, `msg-${i}`)) })
    }

    const logs = store.getLogs('svc-trim')
    // 截断后不超过 MAX_LOGS (5000)
    expect(logs.length).toBeLessThanOrEqual(5000)
    // seenSignatures 不应无限增长——其大小应与 logs 数量接近（允许少量偏差）
    const entry = (store as any).serviceLogs['svc-trim']
    expect(entry.seenSignatures.size).toBeLessThanOrEqual(5000 + 10)
  })
})
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd desktop && pnpm vitest run src/stores/__tests__/log.test.ts
```

预期：FAIL，`seenSignatures.size` 断言失败（当前不清理 Set）。

- [ ] **Step 3: 修改 log.ts**

打开 `desktop/src/stores/log.ts`，做以下两处修改：

**① 将 `MAX_LOGS = 8000` 改为 `5000`，并在其下方添加 `TRIM_BATCH` 常量：**

```typescript
const MAX_LOGS = 5000
const TRIM_BATCH = 500
```

**② 替换 `appendEntry` 函数中的截断逻辑**（原代码为 `if (entry.logs.length > MAX_LOGS) { entry.logs.splice(0, entry.logs.length - MAX_LOGS) }`）：

```typescript
  function appendEntry(entry: ServiceLog, log: LogEntry): boolean {
    const sig = logSignature(log)
    if (entry.seenSignatures.has(sig)) return false
    entry.seenSignatures.add(sig)
    ingest(toDisplayEntry(log), entry.logs)
    if (entry.logs.length > MAX_LOGS) {
      const removed = entry.logs.splice(0, TRIM_BATCH)
      for (const r of removed) entry.seenSignatures.delete(logSignature(r))
    }
    if (entry.oldestLoadedId === null || log.id < entry.oldestLoadedId) {
      entry.oldestLoadedId = log.id
    }
    return true
  }
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd desktop && pnpm vitest run src/stores/__tests__/log.test.ts
```

预期：所有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add desktop/src/stores/log.ts desktop/src/stores/__tests__/log.test.ts
git commit -m "feat(log-store): trim seenSignatures on log eviction, MAX_LOGS 5000"
```

---

## Task 3: deploymentLog.ts — MAX_LOGS 调整 + 批量截断

**Files:**
- Modify: `desktop/src/stores/deploymentLog.ts`
- Test: `desktop/src/stores/__tests__/deploymentLog.test.ts`

- [ ] **Step 1: 写失败测试**

在 `desktop/src/stores/__tests__/deploymentLog.test.ts` 的 `describe('log ingestion', ...)` 块内追加：

```typescript
  it('超出 MAX_LOGS 时截断到不超过 MAX_LOGS 条', () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep-trim')
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1]

    // 注入 5001 条日志
    for (let i = 1; i <= 5001; i++) {
      ws.onmessage?.({ data: JSON.stringify({
        id: i,
        timestamp: `2024-01-01T00:00:${String(i).padStart(5, '0')}Z`,
        message: `msg-${i}`,
        level: 'INFO',
        service_id: '',
        run_id: '',
        stream: 'stdout',
      }) })
    }

    expect(store.getLogs('dep-trim').length).toBeLessThanOrEqual(5000)
  })
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd desktop && pnpm vitest run src/stores/__tests__/deploymentLog.test.ts
```

预期：FAIL，日志数量为 5001 超过期望的 5000。

- [ ] **Step 3: 修改 deploymentLog.ts**

打开 `desktop/src/stores/deploymentLog.ts`：

**① 将 `MAX_LOGS = 8000` 改为 `5000`，在其下方添加常量：**

```typescript
const MAX_LOGS = 5000
const TRIM_BATCH = 500
```

**② 替换 `ingestEntry` 中的截断逻辑**（原为 `if (session.logs.length > MAX_LOGS) { session.logs.splice(0, session.logs.length - MAX_LOGS) }`）：

```typescript
    if (session.logs.length > MAX_LOGS) {
      session.logs.splice(0, TRIM_BATCH)
    }
```

> 注意：`deploymentLog` 没有 `seenSignatures`，通过 `id` 去重（`insertSorted` 中的 `findIndex`），所以只需调整截断批次大小即可，不需要额外清理。

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd desktop && pnpm vitest run src/stores/__tests__/deploymentLog.test.ts
```

预期：所有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add desktop/src/stores/deploymentLog.ts desktop/src/stores/__tests__/deploymentLog.test.ts
git commit -m "feat(deployment-log-store): batch trim, MAX_LOGS 5000"
```

---

## Task 4: LogPanel.vue — 引入虚拟滚动

**Files:**
- Modify: `desktop/src/components/Panel/LogPanel.vue`

这是核心改动，分步完成。

### 4a: 替换模板层（容器结构）

- [ ] **Step 1: 在 `<script setup>` 顶部引入 useVirtualizer**

在 `LogPanel.vue` 的 `<script setup>` 中，在现有 import 后添加：

```typescript
import { useVirtualizer } from '@tanstack/vue-virtual'
```

- [ ] **Step 2: 删除 `scrollToBottom` 中废弃的 querySelector 逻辑，并替换为 virtualizer scrollToIndex**

先在 `<script setup>` 末尾（`const stats = ...` 之后）添加 virtualizer 声明：

```typescript
const virtualizer = useVirtualizer(
  computed(() => ({
    count: displayItems.value.length,
    getScrollElement: () => logListEl.value,
    estimateSize: () => 22,
    overscan: 10,
  }))
)
```

- [ ] **Step 3: 替换 `scrollToBottom` 函数**

将原来的 `scrollToBottom` 函数替换为：

```typescript
async function scrollToBottom() {
  programmaticScroll = true
  await nextTick()
  const count = displayItems.value.length
  if (count > 0) {
    virtualizer.value.scrollToIndex(count - 1, { align: 'end' })
  }
  requestAnimationFrame(() => {
    programmaticScroll = false
  })
}
```

- [ ] **Step 4: 替换 `onScroll` 中的历史加载触发判断**

将原来的 `if (el.scrollTop < 80)` 替换为基于 virtualizer range 的判断：

```typescript
function onScroll() {
  if (programmaticScroll) return
  const el = logListEl.value
  if (!el) return
  const dist = el.scrollHeight - el.scrollTop - el.clientHeight
  const wasFollowing = isFollowing.value
  if (dist >= 50) {
    isFollowing.value = false
    cancelScrollRetries()
  } else {
    isFollowing.value = true
    newLogCount.value = 0
    if (!wasFollowing) pinToBottomIfFollowing()
  }
  const range = virtualizer.value.range
  if (range && range.startIndex < 5) {
    void tryLoadMoreHistory()
  }
}
```

- [ ] **Step 5: 替换 `tryLoadMoreHistory` 中的滚动补偿逻辑**

将 `tryLoadMoreHistory` 函数整体替换为：

```typescript
async function tryLoadMoreHistory() {
  if (props.source?.type === 'deployment') {
    if (!deploymentLogStore.hasMoreHistory(props.source.deploymentId)) return
    if (isLoadingHistory.value) return
    isLoadingHistory.value = true
    const prevStart = virtualizer.value.range?.startIndex ?? 0
    const prevCount = displayItems.value.length
    await deploymentLogStore.loadMoreHistory(props.source.deploymentId)
    await nextTick()
    const added = displayItems.value.length - prevCount
    if (added > 0) {
      programmaticScroll = true
      virtualizer.value.scrollToIndex(prevStart + added, { align: 'start' })
      requestAnimationFrame(() => { programmaticScroll = false })
    }
    isLoadingHistory.value = false
    return
  }
  if (!props.serviceId) return
  if (!logStore.hasMoreHistory(props.serviceId)) return
  if (isLoadingHistory.value) return
  isLoadingHistory.value = true
  const prevStart = virtualizer.value.range?.startIndex ?? 0
  const prevCount = displayItems.value.length
  await logStore.loadMoreHistory(props.serviceId)
  await nextTick()
  const added = displayItems.value.length - prevCount
  if (added > 0) {
    programmaticScroll = true
    virtualizer.value.scrollToIndex(prevStart + added, { align: 'start' })
    requestAnimationFrame(() => { programmaticScroll = false })
  }
  isLoadingHistory.value = false
}
```

- [ ] **Step 6: 替换模板中的日志渲染区域**

将 `<template>` 中的 `.log-list` 内容替换为（保留 loading/history-end 头部提示和 selection-add-btn 不变）：

```html
<div ref="logListEl" class="log-list" @scroll="onScroll" @wheel="onWheel">
  <div v-if="(serviceId || source?.type === 'deployment') && isLoadingHistory" class="history-loading">加载历史记录中…</div>
  <div v-else-if="(serviceId && !logStore.hasMoreHistory(serviceId)) || (source?.type === 'deployment' && !deploymentLogStore.hasMoreHistory(source.deploymentId))" class="history-end">— 已到最早记录 —</div>

  <div :style="{ height: virtualizer.getTotalSize() + 'px', position: 'relative' }">
    <div
      v-for="vRow in virtualizer.getVirtualItems()"
      :key="vRow.key"
      :data-index="vRow.index"
      :ref="(el) => { if (el) virtualizer.measureElement(el as Element) }"
      :style="{ position: 'absolute', top: vRow.start + 'px', width: '100%' }"
    >
      <template v-if="displayItems[vRow.index]">
        <BookmarkMarkerRow
          v-if="displayItems[vRow.index].kind === 'markerStart'"
          :is-start="true"
          :date="(displayItems[vRow.index] as any).date"
        />
        <BookmarkMarkerRow
          v-else-if="displayItems[vRow.index].kind === 'markerEnd'"
          :is-start="false"
          :date="(displayItems[vRow.index] as any).date"
        />
        <LogHistorySeparatorRow
          v-else-if="displayItems[vRow.index].kind === 'historySeparator'"
        />
        <LogRow
          v-else-if="displayItems[vRow.index].kind === 'entry'"
          :log="(displayItems[vRow.index] as any).log"
          :service-name="serviceNameFor((displayItems[vRow.index] as any).log)"
          :highlighted="isHighlighted((displayItems[vRow.index] as any).log)"
          @selection-change="(t, r) => onLogSelection((displayItems[vRow.index] as any).log.id, t, r)"
        />
      </template>
    </div>
  </div>

  <button
    v-if="activeSelectionText && activeSelectionRect"
    class="selection-add-btn"
    :style="selectionButtonStyle"
    title="填入过滤关键词"
    @mousedown.prevent
    @click="fillChipFromSelection"
  >
    +
  </button>
</div>
```

- [ ] **Step 7: 手动验证**

启动 dev server：

```bash
cd desktop && pnpm dev
```

打开应用，切换到有服务的面板，确认：
1. 日志正常显示，样式无异常
2. 快速滚动到底部，follow 模式生效（新日志自动跟随）
3. 滚动到顶部，历史日志加载，视口不跳动
4. 过滤、书签功能正常

- [ ] **Step 8: Commit**

```bash
git add desktop/src/components/Panel/LogPanel.vue
git commit -m "feat(log-panel): virtual scroll via @tanstack/vue-virtual, dynamic row height"
```

---

## Task 5: 运行全量测试并验收

- [ ] **Step 1: 运行所有测试**

```bash
cd desktop && pnpm vitest run
```

预期：所有测试 PASS，无新增失败。

- [ ] **Step 2: 类型检查**

```bash
cd desktop && pnpm vue-tsc --noEmit
```

预期：无类型错误。

- [ ] **Step 3: Commit（如有修复）**

如 Step 1/2 发现问题并修复，追加提交：

```bash
git add -p
git commit -m "fix: resolve type errors after virtual scroll integration"
```
