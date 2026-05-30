# Log Panel History And Separators Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make deployment log panels load 200 historical rows on open, page older history on upward scroll, show a history/live boundary, and show ephemeral start/stop/restart separator rows.

**Architecture:** The Go agent will expose real ID-based `before` pagination through the deployment logs endpoint. The frontend will keep history paging state in `deploymentLog`, panel-local initial history boundaries in `LogPanel.vue`, and ephemeral lifecycle markers in a new Pinia store. Display-only separator rows will be merged by `logDisplay.ts` so they do not affect filtering, bookmarks, export, or stats.

**Tech Stack:** Go HTTP handlers, `logbackend`, SQLite store, Vue 3, Pinia, `@tanstack/vue-virtual`, Vitest, Go tests.

---

## File Structure

- `agent/logbackend/backend.go`: define the deployment history query contract with an ID cursor.
- `agent/api/handler_deployment_logs.go`: parse `before` from query string and pass it to the backend.
- `agent/logbackend/sqlite.go`: forward the ID cursor to `store.Fetch`.
- `agent/logbackend/remote.go`: forward the ID cursor to remote `/api/logs`.
- `agent/api/handler_deployment_logs_test.go`: verify endpoint scoping and `before` parsing.
- `agent/logbackend/sqlite_test.go`: verify SQLite backend returns rows before the cursor.
- `desktop/src/stores/logLifecycle.ts`: new in-memory lifecycle marker store.
- `desktop/src/stores/agent.ts`: record lifecycle markers after successful start/stop/restart API calls.
- `desktop/src/stores/__tests__/agent.test.ts`: verify lifecycle markers are recorded only after successful API calls.
- `desktop/src/stores/deploymentLog.ts`: keep history pagination cursor tied to history loads and return a result from `loadMoreHistory`.
- `desktop/src/stores/__tests__/deploymentLog.test.ts`: verify first/next history requests and cursor behavior.
- `desktop/src/lib/logDisplay.ts`: merge lifecycle marker display items with existing log, bookmark, and history separator items.
- `desktop/src/lib/__tests__/logDisplay.test.ts`: verify history separators and lifecycle separators compose correctly.
- `desktop/src/components/Panel/LogLifecycleSeparatorRow.vue`: render ephemeral lifecycle separator rows.
- `desktop/src/components/Panel/LogPanel.vue`: load initial history, set initial boundary, render lifecycle separators, and preserve scroll position when loading older history.
- `desktop/src/components/Panel/__tests__/LogPanel.test.ts`: verify panel source lifecycle and initial history boundary behavior.

## Task 1: Backend Deployment History Cursor

**Files:**
- Modify: `agent/logbackend/backend.go`
- Modify: `agent/api/handler_deployment_logs.go`
- Modify: `agent/logbackend/sqlite.go`
- Modify: `agent/logbackend/remote.go`
- Test: `agent/api/handler_deployment_logs_test.go`
- Test: `agent/logbackend/sqlite_test.go`

- [ ] **Step 1: Add failing handler test for `before`**

In `agent/api/handler_deployment_logs_test.go`, update `TestDeploymentLogsEndpoint_ScopesQueryToPathDeploymentID` so the request includes `before=88` and the assertion checks the backend filter:

```go
resp, err := http.Get(srv.URL + "/api/deployments/" + depID + "/logs?limit=10&before=88")
require.NoError(t, err)
assert.Equal(t, http.StatusOK, resp.StatusCode)

var result struct {
	Items []model.LogEntry `json:"items"`
}
require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
require.Len(t, result.Items, 1)
assert.Equal(t, depID, backend.queryFilter.DeploymentID)
assert.Equal(t, 10, backend.queryFilter.Limit)
assert.Equal(t, int64(88), backend.queryFilter.BeforeID)
```

- [ ] **Step 2: Add failing SQLite backend cursor test**

In `agent/logbackend/sqlite_test.go`, add this test after `TestSQLiteBackend_QueryReturnsEntries`:

```go
func TestSQLiteBackend_QueryBeforeID(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	now := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 5; i++ {
		buf.Append(model.LogEntry{
			DeploymentID: "svc-1",
			RunID:        "r1",
			Timestamp:    now.Add(time.Duration(i) * time.Millisecond),
			Level:        "INFO",
			Message:      fmt.Sprintf("msg-%d", i),
			Stream:       "stdout",
		})
	}
	time.Sleep(200 * time.Millisecond)

	first, _, err := b.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "svc-1", Limit: 3})
	require.NoError(t, err)
	require.Len(t, first, 3)

	second, _, err := b.Query(context.Background(), logbackend.QueryFilter{
		DeploymentID: "svc-1",
		Limit:        3,
		BeforeID:     first[0].ID,
	})
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Equal(t, []string{"msg-0", "msg-1"}, []string{second[0].Message, second[1].Message})
}
```

Also add `fmt` to the import list in that file.

- [ ] **Step 3: Run backend tests and confirm failure**

Run:

```bash
cd agent && go test ./api ./logbackend -run 'TestDeploymentLogsEndpoint_ScopesQueryToPathDeploymentID|TestSQLiteBackend_QueryBeforeID' -count=1
```

Expected: FAIL because `QueryFilter.BeforeID` does not exist yet.

- [ ] **Step 4: Implement ID cursor contract**

In `agent/logbackend/backend.go`, replace the `Before time.Time` field on `QueryFilter` with:

```go
// BeforeID 游标分页：只返回 id < BeforeID 的记录；0 表示从最新记录开始。
BeforeID int64
```

Remove the now-unused `time` import if `backend.go` no longer needs it outside `SearchQuery` and `Cursor`. Keep `time` if it is still used by `SearchQuery` or `Cursor`.

- [ ] **Step 5: Parse `before` in deployment logs handler**

In `agent/api/handler_deployment_logs.go`, add `before` parsing inside `fetchDeploymentLogs` after the `filter` literal:

```go
if beforeStr := q.Get("before"); beforeStr != "" {
	before, err := strconv.ParseInt(beforeStr, 10, 64)
	if err != nil || before <= 0 {
		jsonError(w, http.StatusBadRequest, "before is invalid")
		return
	}
	filter.BeforeID = before
}
```

`strconv` is already imported in this file.

- [ ] **Step 6: Pass cursor through SQLite backend**

In `agent/logbackend/sqlite.go`, update `Query` to populate `store.FetchParams.Before`:

```go
params := store.FetchParams{
	DeploymentID: f.DeploymentID,
	RunID:        f.RunID,
	Limit:        f.Limit,
	Before:       f.BeforeID,
}
```

Delete the old comment and `_ = f.Before` line.

- [ ] **Step 7: Pass cursor through remote backend**

In `agent/logbackend/remote.go`, update `Query` so the remote request includes `before`:

```go
if f.BeforeID > 0 {
	q.Set("before", strconv.FormatInt(f.BeforeID, 10))
}
```

Place it next to the existing `limit` query code.

- [ ] **Step 8: Run backend tests**

Run:

```bash
cd agent && go test ./api ./logbackend ./store -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit backend cursor changes**

```bash
git add agent/logbackend/backend.go agent/api/handler_deployment_logs.go agent/logbackend/sqlite.go agent/logbackend/remote.go agent/api/handler_deployment_logs_test.go agent/logbackend/sqlite_test.go
git commit -m "fix(agent): page deployment logs with before cursor"
```

## Task 2: Ephemeral Lifecycle Marker Store

**Files:**
- Create: `desktop/src/stores/logLifecycle.ts`
- Modify: `desktop/src/stores/agent.ts`
- Test: `desktop/src/stores/__tests__/agent.test.ts`

- [ ] **Step 1: Add failing store test**

Create `desktop/src/stores/__tests__/agent.test.ts`:

```typescript
/**
 * agentStore 生命周期操作测试
 *
 * 职责：
 *   - 验证 start/stop/restart 成功后记录当前会话内的日志分割 marker
 *
 * 边界：
 *   - 不建立真实 HTTP 连接，API 层通过 mock 验证
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore } from '../agent'
import { useLogLifecycleStore } from '../logLifecycle'
import { api } from '@/api/agent'

vi.mock('@/api/agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/agent')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      startDeployment: vi.fn().mockResolvedValue(undefined),
      stopDeployment: vi.fn().mockResolvedValue(undefined),
      restartDeployment: vi.fn().mockResolvedValue(undefined),
    },
  }
})

describe('agent deployment lifecycle markers', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('records lifecycle markers after successful deployment actions', async () => {
    const agent = useAgentStore()
    const lifecycle = useLogLifecycleStore()

    await agent.startDeployment('dep-1')
    await agent.stopDeployment('dep-1')
    await agent.restartDeployment('dep-1')

    expect(lifecycle.getMarkers('dep-1').map(m => m.kind)).toEqual(['start', 'stop', 'restart'])
  })

  it('does not record a marker when the API call fails', async () => {
    vi.mocked(api.startDeployment).mockRejectedValueOnce(new Error('boom'))
    const agent = useAgentStore()
    const lifecycle = useLogLifecycleStore()

    await expect(agent.startDeployment('dep-1')).rejects.toThrow('boom')

    expect(lifecycle.getMarkers('dep-1')).toEqual([])
  })
})
```

- [ ] **Step 2: Run test and confirm failure**

Run:

```bash
cd desktop && pnpm vitest run src/stores/__tests__/agent.test.ts
```

Expected: FAIL because `../logLifecycle` does not exist.

- [ ] **Step 3: Create lifecycle marker store**

Create `desktop/src/stores/logLifecycle.ts`:

```typescript
// logLifecycleStore 维护当前前端会话内的 deployment 生命周期分割 marker。
//
// 职责：
//   - 记录 start/stop/restart 操作成功后的显示 marker
//   - 按 deploymentId 提供 marker 列表给日志面板渲染
//
// 边界：
//   - 不持久化 marker，刷新或重启应用后允许丢失
//   - 不写入真实日志流，不参与日志过滤、导出或搜索
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type LogLifecycleKind = 'start' | 'stop' | 'restart'

export interface LogLifecycleMarker {
  id: string
  deploymentId: string
  kind: LogLifecycleKind
  createdAt: string
}

const MAX_MARKERS_PER_DEPLOYMENT = 200

export const useLogLifecycleStore = defineStore('logLifecycle', () => {
  const markersByDeployment = ref<Record<string, LogLifecycleMarker[]>>({})

  function recordMarker(deploymentId: string, kind: LogLifecycleKind, at = new Date()) {
    const markers = markersByDeployment.value[deploymentId] ?? []
    markers.push({
      id: crypto.randomUUID(),
      deploymentId,
      kind,
      createdAt: at.toISOString(),
    })
    markersByDeployment.value[deploymentId] = markers.slice(-MAX_MARKERS_PER_DEPLOYMENT)
  }

  function getMarkers(deploymentId: string): LogLifecycleMarker[] {
    return markersByDeployment.value[deploymentId] ?? []
  }

  return {
    markersByDeployment,
    recordMarker,
    getMarkers,
  }
})
```

- [ ] **Step 4: Record markers in agent store**

In `desktop/src/stores/agent.ts`, import the store:

```typescript
import { useLogLifecycleStore } from '@/stores/logLifecycle'
```

Inside `useAgentStore`, create the store:

```typescript
const logLifecycleStore = useLogLifecycleStore()
```

Replace the three deployment action methods with:

```typescript
async function startDeployment(id: string) {
  await api.startDeployment(id)
  logLifecycleStore.recordMarker(id, 'start')
}

async function stopDeployment(id: string) {
  await api.stopDeployment(id)
  logLifecycleStore.recordMarker(id, 'stop')
}

async function restartDeployment(id: string) {
  await api.restartDeployment(id)
  logLifecycleStore.recordMarker(id, 'restart')
}
```

- [ ] **Step 5: Run frontend store test**

Run:

```bash
cd desktop && pnpm vitest run src/stores/__tests__/agent.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit lifecycle store**

```bash
git add desktop/src/stores/logLifecycle.ts desktop/src/stores/agent.ts desktop/src/stores/__tests__/agent.test.ts
git commit -m "feat(desktop): record deployment lifecycle markers"
```

## Task 3: Deployment History Loading Semantics

**Files:**
- Modify: `desktop/src/stores/deploymentLog.ts`
- Test: `desktop/src/stores/__tests__/deploymentLog.test.ts`

- [ ] **Step 1: Update failing tests for history cursor behavior**

In `desktop/src/stores/__tests__/deploymentLog.test.ts`, replace the `passes oldestLoadedId as before cursor` test with:

```typescript
  it('uses the oldest history id as before cursor for subsequent pages', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch
      .mockResolvedValueOnce([
        { id: 5, timestamp: '2024-01-01T00:00:05Z', message: 'e', level: 'info', deployment_id: 'dep1', run_id: '', stream: '' },
        { id: 6, timestamp: '2024-01-01T00:00:06Z', message: 'f', level: 'info', deployment_id: 'dep1', run_id: '', stream: '' },
      ])
      .mockResolvedValueOnce([
        { id: 3, timestamp: '2024-01-01T00:00:03Z', message: 'c', level: 'info', deployment_id: 'dep1', run_id: '', stream: '' },
      ])

    await store.loadMoreHistory('dep1', 200)
    await store.loadMoreHistory('dep1', 200)

    expect(mockFetch).toHaveBeenNthCalledWith(1, expect.objectContaining({
      deploymentId: 'dep1',
      limit: 200,
      before: undefined,
    }))
    expect(mockFetch).toHaveBeenNthCalledWith(2, expect.objectContaining({
      deploymentId: 'dep1',
      limit: 200,
      before: 5,
    }))
  })
```

Add this second test in the same `describe('loadMoreHistory', ...)` block:

```typescript
  it('does not let websocket ids advance the history pagination cursor', async () => {
    const store = useDeploymentLogStore()
    store.subscribe('dep1')
    const ws = MockWebSocket.instances[0]

    ws.onmessage?.({ data: JSON.stringify({
      id: 1,
      timestamp: '2024-01-01T00:00:10Z',
      message: 'live',
      level: 'INFO',
      deployment_id: 'dep1',
      run_id: '',
      stream: 'stdout',
    }) })

    const mockFetch = vi.mocked(apiModule.api.fetchDeploymentLogs)
    mockFetch.mockResolvedValueOnce([
      { id: 20, timestamp: '2024-01-01T00:00:05Z', message: 'history', level: 'INFO', deployment_id: 'dep1', run_id: '', stream: 'stdout' },
    ])

    await store.loadMoreHistory('dep1', 200)

    expect(mockFetch).toHaveBeenCalledWith(expect.objectContaining({ before: undefined }))
  })
```

- [ ] **Step 2: Run tests and confirm failure**

Run:

```bash
cd desktop && pnpm vitest run src/stores/__tests__/deploymentLog.test.ts
```

Expected: FAIL because websocket ingestion currently updates `oldestLoadedId`.

- [ ] **Step 3: Return history load metadata and isolate cursor updates**

In `desktop/src/stores/deploymentLog.ts`, add this interface near `DeploymentSession`:

```typescript
export interface LoadHistoryResult {
  added: number
  entries: DisplayLogEntry[]
}
```

Remove this block from `ingestEntry`:

```typescript
if (session.oldestLoadedId == null || raw.id < session.oldestLoadedId) {
  session.oldestLoadedId = raw.id
}
```

Update `loadMoreHistory` to return metadata and update the cursor only from fetched history entries:

```typescript
  async function loadMoreHistory(deploymentId: string, limit = 200): Promise<LoadHistoryResult> {
    const session = sessions.value.get(deploymentId)
    if (!session || !session.hasMoreHistory || session.loadingMoreHistory) {
      return { added: 0, entries: [] }
    }
    session.loadingMoreHistory = true
    touchSessions()
    try {
      const entries = await api.fetchDeploymentLogs({
        deploymentId,
        limit,
        before: session.oldestLoadedId ?? undefined,
      })
      const displayEntries = entries.map(toDisplayEntry)
      for (let i = entries.length - 1; i >= 0; i--) {
        ingestEntry(session, entries[i])
      }
      for (const entry of entries) {
        if (session.oldestLoadedId == null || entry.id < session.oldestLoadedId) {
          session.oldestLoadedId = entry.id
        }
      }
      session.hasMoreHistory = entries.length >= limit
      return { added: entries.length, entries: displayEntries }
    } catch (err) {
      console.error('Failed to load deployment log history', err)
      return { added: 0, entries: [] }
    } finally {
      session.loadingMoreHistory = false
      touchSessions()
    }
  }
```

- [ ] **Step 4: Run deployment log tests**

Run:

```bash
cd desktop && pnpm vitest run src/stores/__tests__/deploymentLog.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit history store changes**

```bash
git add desktop/src/stores/deploymentLog.ts desktop/src/stores/__tests__/deploymentLog.test.ts
git commit -m "fix(desktop): page deployment history from history cursor"
```

## Task 4: Display Lifecycle Separators

**Files:**
- Modify: `desktop/src/lib/logDisplay.ts`
- Test: `desktop/src/lib/__tests__/logDisplay.test.ts`
- Create: `desktop/src/components/Panel/LogLifecycleSeparatorRow.vue`

- [ ] **Step 1: Add failing display tests**

In `desktop/src/lib/__tests__/logDisplay.test.ts`, add these tests inside `describe('makeDisplayItems', ...)`:

```typescript
  it('按时间插入生命周期分隔线', () => {
    const logs = [
      makeLog(1, '2026-05-21T10:00:01.000Z'),
      makeLog(2, '2026-05-21T10:00:03.000Z'),
    ]

    const items = makeDisplayItems(logs, null, markers, null, [
      { id: 'life-1', deploymentId: 'dep-1', kind: 'restart', createdAt: '2026-05-21T10:00:02.000Z' },
    ])

    expect(items.map(item => item.kind)).toEqual(['entry', 'lifecycleSeparator', 'entry'])
  })

  it('生命周期分隔线不参与统计', () => {
    const items = makeDisplayItems([makeLog(1, '2026-05-21T10:00:01.000Z')], null, markers, null, [
      { id: 'life-1', deploymentId: 'dep-1', kind: 'start', createdAt: '2026-05-21T10:00:02.000Z' },
    ])

    expect(computeDisplayStats(items).total).toBe(1)
  })
```

- [ ] **Step 2: Run display tests and confirm failure**

Run:

```bash
cd desktop && pnpm vitest run src/lib/__tests__/logDisplay.test.ts
```

Expected: FAIL because `lifecycleSeparator` is not part of `LogDisplayItem`.

- [ ] **Step 3: Extend log display items**

In `desktop/src/lib/logDisplay.ts`, import the lifecycle marker type:

```typescript
import type { LogLifecycleMarker } from '@/stores/logLifecycle'
```

Extend `LogDisplayItem`:

```typescript
  | { kind: 'lifecycleSeparator'; id: string; marker: LogLifecycleMarker }
```

Add this helper after `withHistorySeparator`:

```typescript
function withLifecycleSeparators(
  items: LogDisplayItem[],
  markers: LogLifecycleMarker[] = [],
): LogDisplayItem[] {
  if (!markers.length) return items
  const out = [...items]
  const sorted = [...markers].sort(
    (a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime(),
  )
  for (const marker of sorted) {
    const markerTime = new Date(marker.createdAt).getTime()
    const insertAt = out.findIndex(item =>
      item.kind === 'entry' && new Date(item.log.timestamp).getTime() > markerTime
    )
    const displayItem: LogDisplayItem = {
      kind: 'lifecycleSeparator',
      id: `lifecycle-${marker.id}`,
      marker,
    }
    if (insertAt < 0) out.push(displayItem)
    else out.splice(insertAt, 0, displayItem)
  }
  return out
}
```

Update the `makeDisplayItems` signature:

```typescript
export function makeDisplayItems(
  logs: DisplayLogEntry[],
  bm: BookmarkDisplayInput | null,
  markerIds: MarkerIds,
  historyBoundary: HistoryBoundary | null = null,
  lifecycleMarkers: LogLifecycleMarker[] = [],
): LogDisplayItem[] {
```

For every existing `return withHistorySeparator(items, historyBoundary)`, wrap it as:

```typescript
return withLifecycleSeparators(withHistorySeparator(items, historyBoundary), lifecycleMarkers)
```

- [ ] **Step 4: Create lifecycle separator row component**

Create `desktop/src/components/Panel/LogLifecycleSeparatorRow.vue`:

```vue
<!--
生命周期日志分隔行

职责：
  - 在当前前端会话中标记 deployment 启动、停止、重启操作

边界：
  - 不代表真实 LogEntry
  - 不持久化，不参与复制、导出或过滤
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { LogLifecycleMarker } from '@/stores/logLifecycle'

const props = defineProps<{
  marker: LogLifecycleMarker
}>()

const label = computed(() => {
  if (props.marker.kind === 'start') return '启动'
  if (props.marker.kind === 'stop') return '停止'
  return '重启'
})

const time = computed(() => {
  const d = new Date(props.marker.createdAt)
  return d.toLocaleTimeString('en-US', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
})
</script>

<template>
  <div class="lifecycle-separator-row" :class="marker.kind">
    <span class="line" />
    <span class="label">{{ label }} · {{ time }}</span>
    <span class="line" />
  </div>
</template>

<style scoped>
.lifecycle-separator-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  color: var(--text-tertiary);
  font-size: 10px;
}
.line {
  height: 1px;
  flex: 1;
  background: var(--border-secondary);
}
.label {
  white-space: nowrap;
}
.start .label { color: #3fb950; }
.stop .label { color: #f85149; }
.restart .label { color: #d29922; }
</style>
```

- [ ] **Step 5: Run display tests**

Run:

```bash
cd desktop && pnpm vitest run src/lib/__tests__/logDisplay.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit display separator changes**

```bash
git add desktop/src/lib/logDisplay.ts desktop/src/lib/__tests__/logDisplay.test.ts desktop/src/components/Panel/LogLifecycleSeparatorRow.vue
git commit -m "feat(desktop): render lifecycle log separators"
```

## Task 5: Log Panel Initial History Boundary

**Files:**
- Modify: `desktop/src/components/Panel/LogPanel.vue`
- Test: `desktop/src/components/Panel/__tests__/LogPanel.test.ts`

- [ ] **Step 1: Add failing panel tests**

In `desktop/src/components/Panel/__tests__/LogPanel.test.ts`, update the existing source-switch test to expect the initial load limit:

```typescript
expect(loadMoreHistory).toHaveBeenCalledWith('dep-1', 200)
```

and:

```typescript
expect(loadMoreHistory).toHaveBeenCalledWith('dep-2', 200)
```

Add this test to the same file:

```typescript
  it('首次历史加载完成后刷新显示列表以插入历史分隔线', async () => {
    const deploymentLogStore = useDeploymentLogStore()
    vi.spyOn(deploymentLogStore, 'subscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'unsubscribe').mockImplementation(() => {})
    vi.spyOn(deploymentLogStore, 'getLogs').mockReturnValue([
      {
        id: 7,
        deployment_id: 'dep-1',
        run_id: 'run-1',
        timestamp: '2026-05-30T10:00:00.000Z',
        level: 'INFO',
        message: 'history',
        stream: 'stdout',
        normalized_message: 'history',
      },
    ])
    vi.spyOn(deploymentLogStore, 'loadMoreHistory').mockResolvedValue({
      added: 1,
      entries: [],
    })

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'panel-1',
        projectId: null,
        source: { type: 'deployment', deploymentId: 'dep-1' },
      },
      global: {
        stubs: {
          PanelToolbar: { template: '<div />' },
          LogRow: { template: '<div />' },
          BookmarkMarkerRow: { template: '<div />' },
          LogHistorySeparatorRow: { template: '<div data-test="history-separator" />' },
          LogLifecycleSeparatorRow: { template: '<div />' },
        },
      },
    })

    await nextTick()
    await Promise.resolve()
    await nextTick()

    expect(wrapper.exists()).toBe(true)
    expect(deploymentLogStore.loadMoreHistory).toHaveBeenCalledWith('dep-1', 200)
  })
```

- [ ] **Step 2: Run panel tests and confirm failure**

Run:

```bash
cd desktop && pnpm vitest run src/components/Panel/__tests__/LogPanel.test.ts
```

Expected: FAIL because the component still calls `loadMoreHistory(deploymentId)` without an explicit limit.

- [ ] **Step 3: Update LogPanel imports and state**

In `desktop/src/components/Panel/LogPanel.vue`, import the lifecycle store and row component:

```typescript
import { useLogLifecycleStore } from '@/stores/logLifecycle'
import LogLifecycleSeparatorRow from './LogLifecycleSeparatorRow.vue'
import type { HistoryBoundary } from '@/lib/logDisplay'
```

Create constants and state near the existing refs:

```typescript
const INITIAL_HISTORY_LIMIT = 200
const logLifecycleStore = useLogLifecycleStore()
const initialHistoryBoundary = ref<HistoryBoundary | null>(null)
let historyLoadToken = 0
```

- [ ] **Step 4: Make initial subscription load explicit and boundary-aware**

Replace `subscribeDeployment` in `LogPanel.vue` with:

```typescript
async function subscribeDeployment(deploymentId: string) {
  deploymentLogStore.subscribe(deploymentId)
  initialHistoryBoundary.value = null
  const token = ++historyLoadToken
  await deploymentLogStore.loadMoreHistory(deploymentId, INITIAL_HISTORY_LIMIT)
  if (token !== historyLoadToken || deploymentId !== deploymentIdFromSource(props.source)) return
  const logs = deploymentLogStore.getLogs(deploymentId)
  const newest = logs[logs.length - 1]
  initialHistoryBoundary.value = newest ? { timestamp: newest.timestamp, id: newest.id } : null
  refreshDisplayImmediately()
  if (isFollowing.value) pinToBottomIfFollowing()
}
```

In the source watcher, before subscribing to the new source, reset the token and boundary:

```typescript
historyLoadToken++
initialHistoryBoundary.value = null
```

- [ ] **Step 5: Pass history and lifecycle markers into display construction**

Replace the `historyBoundary` computed with:

```typescript
const historyBoundary = computed(() => initialHistoryBoundary.value)

const lifecycleMarkers = computed(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  return deploymentId ? logLifecycleStore.getMarkers(deploymentId) : []
})
```

Update `makeDisplayItems` call:

```typescript
const items = makeDisplayItems(logs, displayBm, {
  start: markerStartId.value,
  end: markerEndId.value,
}, historyBoundary.value, lifecycleMarkers.value)
```

Add a watcher so marker changes refresh the panel:

```typescript
watch(
  lifecycleMarkers,
  () => scheduleDisplayRefresh(),
  { deep: true },
)
```

- [ ] **Step 6: Render lifecycle separator rows**

In the template virtual row block, add this case after `LogHistorySeparatorRow`:

```vue
<LogLifecycleSeparatorRow
  v-else-if="displayItems[vRow.index].kind === 'lifecycleSeparator'"
  :marker="(displayItems[vRow.index] as any).marker"
/>
```

Add `LogLifecycleSeparatorRow` to the test stubs.

- [ ] **Step 7: Make manual history loading use the explicit page size**

In `tryLoadMoreHistory`, update the load call:

```typescript
await deploymentLogStore.loadMoreHistory(props.source.deploymentId, INITIAL_HISTORY_LIMIT)
```

Keep the existing `prevStart + added` scroll compensation.

- [ ] **Step 8: Run panel tests**

Run:

```bash
cd desktop && pnpm vitest run src/components/Panel/__tests__/LogPanel.test.ts
```

Expected: PASS.

- [ ] **Step 9: Commit panel changes**

```bash
git add desktop/src/components/Panel/LogPanel.vue desktop/src/components/Panel/__tests__/LogPanel.test.ts
git commit -m "feat(desktop): show log history and lifecycle separators"
```

## Task 6: Integration Verification

**Files:**
- No new files.

- [ ] **Step 1: Run focused Go verification**

Run:

```bash
cd agent && go test ./api ./logbackend ./store -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused desktop verification**

Run:

```bash
cd desktop && pnpm vitest run src/stores/__tests__/agent.test.ts src/stores/__tests__/deploymentLog.test.ts src/lib/__tests__/logDisplay.test.ts src/components/Panel/__tests__/LogPanel.test.ts src/api/__tests__/agent-deployment.test.ts
```

Expected: PASS.

- [ ] **Step 3: Run desktop build**

Run:

```bash
cd desktop && pnpm build
```

Expected: PASS. If unrelated existing worktree changes break the build, record the exact failure and keep the focused test results.

- [ ] **Step 4: Final status check**

Run:

```bash
git status --short
```

Expected: only intentional changes are present. Existing unrelated user changes may remain dirty and must not be reverted.
