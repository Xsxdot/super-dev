# Project Tabs Trace Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build project-scoped workspace tabs and a cross-service trace search investigation board for SuperDev desktop.

**Architecture:** Add two Go agent read APIs first: keyword search across a project's services and time-anchored context across those services. Then add a desktop workspace layer above the existing panel layout, with project tabs for service logs and per-click search tabs for investigation boards. Search results use a left navigation rail and right synchronized service columns built from time buckets.

**Tech Stack:** Go 1.22 HTTP handlers + SQLite store, Vue 3 + Pinia + Vitest, existing Tauri desktop frontend.

---

## File Structure

### Agent

- Modify `agent/store/store.go`
  - Add `SearchParams`, `SearchResult`, `ContextParams`, `ContextResult`.
  - Add `Search(p SearchParams)` and `FetchContext(p ContextParams)`.
- Modify `agent/store/store_test.go`
  - Cover keyword search, service filtering, service counts, chronological order, target context.
- Create `agent/api/handler_log_search.go`
  - Owns `/api/log-search` and `/api/logs/context`.
  - Keeps HTTP parsing out of `store`.
- Modify `agent/api/server.go`
  - Register the two new routes.
- Create `agent/api/handler_log_search_test.go`
  - Same-package API tests because app internals are needed to seed store/projects without exposing test-only API.

### Desktop Stores And API

- Modify `desktop/src/api/agent.ts`
  - Add search/context response types and API functions.
- Modify `desktop/src/stores/panel.ts`
  - Export `createEmptyPanelRoot()` and add `setRoot(root, focusedPanelId?)` so workspace tabs can swap panel layouts.
- Create `desktop/src/stores/workspace.ts`
  - Owns workspace tabs, active tab, project tab creation, search tab creation, service opening, search execution, context loading, hidden services, pinned services.
- Create `desktop/src/stores/__tests__/workspace.test.ts`
  - Covers project tab reuse, search tab multiplicity, service open behavior, hidden/pinned service state.

### Desktop Search Board

- Create `desktop/src/lib/searchBuckets.ts`
  - Pure functions for 1-second time bucket creation and aligned blank cells.
- Create `desktop/src/lib/__tests__/searchBuckets.test.ts`
  - Covers skipped all-empty buckets, per-service blanks, bucket height inputs.
- Create `desktop/src/components/Workspace/WorkspaceShell.vue`
  - Replaces direct `PanelLayout` usage in `MainPage`.
- Create `desktop/src/components/Workspace/WorkspaceTabs.vue`
  - Renders project/search tabs and close actions.
- Create `desktop/src/components/Search/SearchPage.vue`
  - Search empty/loading/error/results state and search input.
- Create `desktop/src/components/Search/SearchBoard.vue`
  - Layout: left service rail + timeline, right synchronized service columns.
- Create `desktop/src/components/Search/SearchServiceRail.vue`
  - Service counts and hide/show toggles.
- Create `desktop/src/components/Search/SearchTimeline.vue`
  - Match-only timeline; clicking a result loads context.
- Create `desktop/src/components/Search/SearchServiceColumns.vue`
  - Renders synchronized columns, pinned service behavior, target highlight.

### Desktop Integration

- Modify `desktop/src/pages/MainPage.vue`
  - Render `WorkspaceShell` instead of `PanelLayout`.
- Modify `desktop/src/components/Sidebar/SidebarView.vue`
  - Service click calls workspace store instead of directly replacing panel scope.
- Modify `desktop/src/components/Sidebar/ProjectHeader.vue`
  - Add search button, emits `search`.

---

## Task 1: Agent Store Search And Context

**Files:**
- Modify: `agent/store/store.go`
- Modify: `agent/store/store_test.go`

- [ ] **Step 1: Add failing store tests for search**

Append these tests to `agent/store/store_test.go`:

```go
func TestSearchFindsKeywordAcrossServicesInTimeOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-8f21 api done", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: base.Add(1 * time.Second), Level: "WARN", Message: "TRACE-8F21 worker retry", Stream: "stderr"},
		{ServiceID: "svc-c", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "unrelated", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		ServiceIDs: []string{"svc-a", "svc-b", "svc-c"},
		Query:     "trace-8f21",
		Limit:     10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 2)
	assert.Equal(t, "svc-b", got.Entries[0].ServiceID)
	assert.Equal(t, "svc-a", got.Entries[1].ServiceID)
	assert.Equal(t, 2, got.Total)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, got.ServiceCounts)
}

func TestSearchRestrictsToServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 api", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 worker", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		ServiceIDs: []string{"svc-b"},
		Query:     "trace-8f21",
		Limit:     10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 1)
	assert.Equal(t, "svc-b", got.Entries[0].ServiceID)
	assert.Equal(t, map[string]int{"svc-b": 1}, got.ServiceCounts)
}
```

- [ ] **Step 2: Run store search tests and verify they fail**

Run:

```bash
cd agent
go test ./store -run 'TestSearch' -count=1
```

Expected: FAIL with errors like `s.Search undefined` and `undefined: store.SearchParams`.

- [ ] **Step 3: Implement search types and method**

In `agent/store/store.go`, add this import:

```go
import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)
```

Replace the current import block with the above if needed, preserving `github.com/superdev/agent/model` and `_ "modernc.org/sqlite"`.

Add these types after `FetchParams`:

```go
// SearchParams 定义跨服务历史日志搜索参数。
//
// ServiceIDs 为空时直接返回空结果，避免无边界全库搜索。
// Query 会做大小写不敏感的 message 包含匹配。
type SearchParams struct {
	ServiceIDs []string
	Query      string
	Limit      int
	Before     int64
	From       *time.Time
	To         *time.Time
}

// SearchResult 表示一次日志搜索的结果和按服务聚合的命中数。
type SearchResult struct {
	Entries       []model.LogEntry
	Total         int
	ServiceCounts map[string]int
}
```

Add helper functions near `Fetch`:

```go
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func appendServiceArgs(args []any, serviceIDs []string) []any {
	for _, id := range serviceIDs {
		args = append(args, id)
	}
	return args
}
```

Add the search method after `Fetch`:

```go
// Search 在指定服务集合内按关键词搜索历史日志。
//
// 参数：
//   - p: ServiceIDs 限定搜索范围，Query 为大小写不敏感关键词，Limit 控制返回条数
//
// 返回：
//   - Entries: 按 timestamp ASC, id ASC 排序的匹配日志
//   - Total: 未分页前的总命中数
//   - ServiceCounts: 未分页前按 service_id 聚合的命中数
func (s *Store) Search(p SearchParams) (SearchResult, error) {
	result := SearchResult{
		Entries:       []model.LogEntry{},
		ServiceCounts: map[string]int{},
	}
	queryText := strings.TrimSpace(p.Query)
	if len(p.ServiceIDs) == 0 || queryText == "" {
		return result, nil
	}
	if p.Limit <= 0 {
		p.Limit = 1000
	}

	where := fmt.Sprintf("service_id IN (%s) AND LOWER(message) LIKE LOWER(?)", placeholders(len(p.ServiceIDs)))
	args := appendServiceArgs([]any{}, p.ServiceIDs)
	args = append(args, "%"+queryText+"%")
	if p.From != nil {
		where += " AND timestamp >= ?"
		args = append(args, p.From.UTC())
	}
	if p.To != nil {
		where += " AND timestamp <= ?"
		args = append(args, p.To.UTC())
	}
	if p.Before > 0 {
		where += " AND id < ?"
		args = append(args, p.Before)
	}

	countQuery := "SELECT service_id, COUNT(*) FROM log_entries WHERE " + where + " GROUP BY service_id"
	countRows, err := s.db.Query(countQuery, args...)
	if err != nil {
		return result, err
	}
	defer countRows.Close()
	for countRows.Next() {
		var serviceID string
		var count int
		if err := countRows.Scan(&serviceID, &count); err != nil {
			return result, err
		}
		result.ServiceCounts[serviceID] = count
		result.Total += count
	}
	if err := countRows.Err(); err != nil {
		return result, err
	}

	entryQuery := fmt.Sprintf(
		"SELECT id, service_id, run_id, timestamp, level, message, stream FROM log_entries WHERE %s ORDER BY timestamp ASC, id ASC LIMIT %d",
		where,
		p.Limit,
	)
	rows, err := s.db.Query(entryQuery, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var e model.LogEntry
		if err := rows.Scan(&e.ID, &e.ServiceID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, e)
	}
	return result, rows.Err()
}
```

- [ ] **Step 4: Run store search tests and verify they pass**

Run:

```bash
cd agent
go test ./store -run 'TestSearch' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add failing store tests for context**

Append to `agent/store/store_test.go`:

```go
func TestFetchContextReturnsProjectServicesAroundTargetTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "api before", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: base.Add(-500 * time.Millisecond), Level: "INFO", Message: "worker before", Stream: "stdout"},
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "trace-8f21 target", Stream: "stderr"},
		{ServiceID: "svc-c", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "billing after", Stream: "stdout"},
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Minute), Level: "INFO", Message: "outside window", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{ServiceIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	targetID := search.Entries[0].ID

	got, err := s.FetchContext(store.ContextParams{
		TargetID:   targetID,
		ServiceIDs: []string{"svc-a", "svc-b", "svc-c"},
		Before:     3 * time.Second,
		After:      3 * time.Second,
	})
	require.NoError(t, err)

	assert.Equal(t, targetID, got.TargetID)
	assert.Equal(t, base, got.AnchorTime)
	assert.Equal(t, []string{"api before", "trace-8f21 target"}, messagesOf(got.ItemsByService["svc-a"]))
	assert.Equal(t, []string{"worker before"}, messagesOf(got.ItemsByService["svc-b"]))
	assert.Equal(t, []string{"billing after"}, messagesOf(got.ItemsByService["svc-c"]))
}

func TestFetchContextRejectsTargetOutsideServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "target", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{ServiceIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	_, err = s.FetchContext(store.ContextParams{
		TargetID:   search.Entries[0].ID,
		ServiceIDs: []string{"svc-b"},
		Before:     time.Second,
		After:      time.Second,
	})
	require.ErrorIs(t, err, store.ErrLogEntryNotFound)
}

func messagesOf(entries []model.LogEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Message
	}
	return out
}
```

- [ ] **Step 6: Run context tests and verify they fail**

Run:

```bash
cd agent
go test ./store -run 'TestFetchContext' -count=1
```

Expected: FAIL with `s.FetchContext undefined`, `undefined: store.ContextParams`, and `undefined: store.ErrLogEntryNotFound`.

- [ ] **Step 7: Implement context types and method**

In `agent/store/store.go`, add this var after imports:

```go
// ErrLogEntryNotFound 表示目标日志不存在，或不属于允许查询的服务集合。
var ErrLogEntryNotFound = sql.ErrNoRows
```

Add these types after `SearchResult`:

```go
// ContextParams 定义以某条日志为锚点的跨服务上下文查询参数。
type ContextParams struct {
	TargetID   int64
	ServiceIDs []string
	Before     time.Duration
	After      time.Duration
}

// ContextResult 表示跨服务上下文查询结果。
type ContextResult struct {
	TargetID       int64
	AnchorTime     time.Time
	ItemsByService map[string][]model.LogEntry
}
```

Add this method after `Search`:

```go
// FetchContext 以目标日志时间为锚点，拉取指定服务集合在时间窗口内的日志。
//
// 参数：
//   - p: TargetID 为锚点日志 ID，ServiceIDs 限定项目服务集合，Before/After 控制时间窗口
//
// 返回：
//   - 按 service_id 分组的日志上下文
//   - 目标日志不存在或不属于 ServiceIDs 时返回 ErrLogEntryNotFound
func (s *Store) FetchContext(p ContextParams) (ContextResult, error) {
	result := ContextResult{
		TargetID:       p.TargetID,
		ItemsByService: map[string][]model.LogEntry{},
	}
	if p.TargetID <= 0 || len(p.ServiceIDs) == 0 {
		return result, ErrLogEntryNotFound
	}
	if p.Before <= 0 {
		p.Before = 30 * time.Second
	}
	if p.After <= 0 {
		p.After = 30 * time.Second
	}

	targetQuery := fmt.Sprintf(
		"SELECT timestamp FROM log_entries WHERE id = ? AND service_id IN (%s)",
		placeholders(len(p.ServiceIDs)),
	)
	args := []any{p.TargetID}
	args = appendServiceArgs(args, p.ServiceIDs)
	if err := s.db.QueryRow(targetQuery, args...).Scan(&result.AnchorTime); err != nil {
		if err == sql.ErrNoRows {
			return result, ErrLogEntryNotFound
		}
		return result, err
	}

	for _, serviceID := range p.ServiceIDs {
		result.ItemsByService[serviceID] = []model.LogEntry{}
	}

	from := result.AnchorTime.Add(-p.Before)
	to := result.AnchorTime.Add(p.After)
	contextQuery := fmt.Sprintf(`
		SELECT id, service_id, run_id, timestamp, level, message, stream
		FROM log_entries
		WHERE service_id IN (%s) AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC, id ASC
	`, placeholders(len(p.ServiceIDs)))
	contextArgs := appendServiceArgs([]any{}, p.ServiceIDs)
	contextArgs = append(contextArgs, from, to)
	rows, err := s.db.Query(contextQuery, contextArgs...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var e model.LogEntry
		if err := rows.Scan(&e.ID, &e.ServiceID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return result, err
		}
		result.ItemsByService[e.ServiceID] = append(result.ItemsByService[e.ServiceID], e)
	}
	return result, rows.Err()
}
```

- [ ] **Step 8: Run store tests**

Run:

```bash
cd agent
go test ./store -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

Run:

```bash
git add agent/store/store.go agent/store/store_test.go
git commit -m "feat(agent): add log search store queries"
```

---

## Task 2: Agent HTTP Search And Context API

**Files:**
- Create: `agent/api/handler_log_search.go`
- Create: `agent/api/handler_log_search_test.go`
- Modify: `agent/api/server.go`

- [ ] **Step 1: Add failing API tests**

Create `agent/api/handler_log_search_test.go`:

```go
// Package api 测试日志搜索 HTTP 接口。
//
// 职责：
//   - 验证项目级日志搜索接口
//   - 验证跨服务上下文接口
//
// 边界：
//   - 使用 httptest，不启动真实网络服务
//   - 直接种入 App 内部 store 和 projects，避免暴露测试专用 API
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

func newSearchTestServer(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	app.projects = []model.Project{
		{
			ID:       "proj-1",
			Name:     "Project",
			RootPath: t.TempDir(),
			Services: []model.Service{
				{ID: "svc-a", ProjectID: "proj-1", Name: "api"},
				{ID: "svc-b", ProjectID: "proj-1", Name: "worker"},
				{ID: "svc-c", ProjectID: "proj-1", Name: "billing"},
			},
		},
	}
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		srv.Close()
		app.Close()
	})
	return app, srv
}

func TestLogSearchAPI(t *testing.T) {
	app, srv := newSearchTestServer(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "trace-8f21 api", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-8f21 worker", Stream: "stdout"},
		{ServiceID: "other", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "trace-8f21 outside", Stream: "stdout"},
	}))

	resp, err := http.Get(srv.URL + "/api/log-search?project=proj-1&q=trace-8f21")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logSearchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "trace-8f21", body.Query)
	assert.Equal(t, 2, body.Total)
	require.Len(t, body.Items, 2)
	assert.Equal(t, "svc-a", body.Items[0].ServiceID)
	assert.Equal(t, "svc-b", body.Items[1].ServiceID)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, body.ServiceCounts)
}

func TestLogSearchAPIRequiresProjectAndQuery(t *testing.T) {
	_, srv := newSearchTestServer(t)

	resp, err := http.Get(srv.URL + "/api/log-search?project=proj-1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp2, err := http.Get(srv.URL + "/api/log-search?q=trace")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

func TestLogContextAPI(t *testing.T) {
	app, srv := newSearchTestServer(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "trace-8f21 target", Stream: "stderr"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "worker context", Stream: "stdout"},
	}))
	search, err := app.store.Search(store.SearchParams{ServiceIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/api/logs/context?project=proj-1&id=" + strconv.FormatInt(search.Entries[0].ID, 10))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logContextResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, search.Entries[0].ID, body.TargetID)
	assert.Equal(t, base, body.AnchorTime)
	assert.Len(t, body.ItemsByService["svc-a"], 1)
	assert.Len(t, body.ItemsByService["svc-b"], 1)
	assert.Len(t, body.ItemsByService["svc-c"], 0)
}
```

- [ ] **Step 2: Run API tests and verify they fail**

Run:

```bash
cd agent
go test ./api -run 'TestLog(Search|Context)' -count=1
```

Expected: FAIL because `logSearchResponse`, `logContextResponse`, and routes are undefined.

- [ ] **Step 3: Implement log search handler**

Create `agent/api/handler_log_search.go`:

```go
// Package api provides HTTP handlers for project-scoped log search.
//
// Responsibilities:
//   - Parse log search and context query parameters
//   - Resolve project service scope before querying the store
//   - Return raw log entries for desktop-side investigation UI
//
// Boundaries:
//   - Does not apply project log filter rules
//   - Does not format or group logs for the UI time grid
//   - Does not expose store internals outside HTTP response DTOs
package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

const (
	defaultSearchLimit = 1000
	maxSearchLimit     = 5000
	defaultContextMS   = 30000
	maxContextMS       = 300000
)

type logSearchResponse struct {
	Query         string           `json:"query"`
	Total         int              `json:"total"`
	Items         []model.LogEntry `json:"items"`
	ServiceCounts map[string]int   `json:"service_counts"`
}

type logContextResponse struct {
	TargetID       int64                       `json:"target_id"`
	AnchorTime     time.Time                   `json:"anchor_time"`
	ItemsByService map[string][]model.LogEntry `json:"items_by_service"`
}

func (a *App) searchLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	queryText := strings.TrimSpace(q.Get("q"))
	if projectID == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	if queryText == "" {
		jsonError(w, http.StatusBadRequest, "q is required")
		return
	}

	serviceIDs, ok := a.projectServiceIDs(projectID, q["service"])
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	limit := parseBoundedInt(q.Get("limit"), defaultSearchLimit, maxSearchLimit)
	result, err := a.store.Search(store.SearchParams{
		ServiceIDs: serviceIDs,
		Query:      queryText,
		Limit:      limit,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to search logs: "+err.Error())
		return
	}
	jsonOK(w, logSearchResponse{
		Query:         queryText,
		Total:         result.Total,
		Items:         result.Entries,
		ServiceCounts: result.ServiceCounts,
	})
}

func (a *App) fetchLogContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	if projectID == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	targetID, err := strconv.ParseInt(q.Get("id"), 10, 64)
	if err != nil || targetID <= 0 {
		jsonError(w, http.StatusBadRequest, "id is required")
		return
	}
	serviceIDs, ok := a.projectServiceIDs(projectID, q["service"])
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	beforeMS := parseBoundedInt(q.Get("before_ms"), defaultContextMS, maxContextMS)
	afterMS := parseBoundedInt(q.Get("after_ms"), defaultContextMS, maxContextMS)

	result, err := a.store.FetchContext(store.ContextParams{
		TargetID:   targetID,
		ServiceIDs: serviceIDs,
		Before:     time.Duration(beforeMS) * time.Millisecond,
		After:      time.Duration(afterMS) * time.Millisecond,
	})
	if errors.Is(err, store.ErrLogEntryNotFound) {
		jsonError(w, http.StatusNotFound, "log entry not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch log context: "+err.Error())
		return
	}
	jsonOK(w, logContextResponse{
		TargetID:       result.TargetID,
		AnchorTime:     result.AnchorTime,
		ItemsByService: result.ItemsByService,
	})
}

func (a *App) projectServiceIDs(projectID string, requested []string) ([]string, bool) {
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, service := range project.Services {
		allowed[service.ID] = true
	}
	if len(requested) == 0 {
		ids := make([]string, 0, len(project.Services))
		for _, service := range project.Services {
			ids = append(ids, service.ID)
		}
		return ids, true
	}
	ids := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, id := range requested {
		if !allowed[id] || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, true
}

func parseBoundedInt(raw string, fallback int, max int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
```

- [ ] **Step 4: Register routes**

In `agent/api/server.go`, add under the existing log route registration:

```go
mux.HandleFunc("GET /api/log-search", a.searchLogs)
mux.HandleFunc("GET /api/logs/context", a.fetchLogContext)
```

- [ ] **Step 5: Run API tests**

Run:

```bash
cd agent
gofmt -w api/handler_log_search.go api/handler_log_search_test.go api/server.go
go test ./api -run 'TestLog(Search|Context)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all agent tests**

Run:

```bash
cd agent
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add agent/api/handler_log_search.go agent/api/handler_log_search_test.go agent/api/server.go
git commit -m "feat(agent): expose project log search api"
```

---

## Task 3: Desktop API And Search Bucket Utilities

**Files:**
- Modify: `desktop/src/api/agent.ts`
- Create: `desktop/src/lib/searchBuckets.ts`
- Create: `desktop/src/lib/__tests__/searchBuckets.test.ts`

- [ ] **Step 1: Add failing bucket tests**

Create `desktop/src/lib/__tests__/searchBuckets.test.ts`:

```ts
/**
 * searchBuckets 测试跨服务日志上下文的时间栅格对齐。
 *
 * 职责：
 *   - 验证 1 秒时间栅格按服务对齐
 *   - 验证所有服务都没有日志的时间段不会产生栅格
 *   - 验证单个服务缺日志时产生空白占位
 *
 * 边界：
 *   - 不测 DOM 高度，组件层根据 row.entries 计算实际高度
 *   - 不负责搜索 API 请求
 */
import { describe, expect, it } from 'vitest'
import { buildSearchBuckets } from '../searchBuckets'
import type { LogEntry } from '../../api/agent'

function log(id: number, serviceId: string, timestamp: string, message: string): LogEntry {
  return {
    id,
    service_id: serviceId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('buildSearchBuckets', () => {
  it('按 1 秒时间栅格生成跨服务行，并给缺日志服务留空白', () => {
    const buckets = buildSearchBuckets({
      serviceIds: ['svc-a', 'svc-b', 'svc-c'],
      itemsByService: {
        'svc-a': [log(1, 'svc-a', '2026-05-20T22:41:32.100Z', 'a')],
        'svc-b': [log(2, 'svc-b', '2026-05-20T22:41:32.300Z', 'b')],
        'svc-c': [],
      },
    })

    expect(buckets).toHaveLength(1)
    expect(buckets[0].bucketLabel).toBe('22:41:32')
    expect(buckets[0].cells['svc-a'].entries.map(e => e.message)).toEqual(['a'])
    expect(buckets[0].cells['svc-b'].entries.map(e => e.message)).toEqual(['b'])
    expect(buckets[0].cells['svc-c'].entries).toEqual([])
    expect(buckets[0].cells['svc-c'].blank).toBe(true)
  })

  it('不会为所有服务都没有日志的秒生成空栅格', () => {
    const buckets = buildSearchBuckets({
      serviceIds: ['svc-a', 'svc-b'],
      itemsByService: {
        'svc-a': [log(1, 'svc-a', '2026-05-20T22:41:33.100Z', 'a')],
        'svc-b': [],
      },
    })

    expect(buckets.map(b => b.bucketLabel)).toEqual(['22:41:33'])
  })

  it('同一服务同一秒内多条日志保留原顺序', () => {
    const buckets = buildSearchBuckets({
      serviceIds: ['svc-a'],
      itemsByService: {
        'svc-a': [
          log(1, 'svc-a', '2026-05-20T22:41:32.100Z', 'first'),
          log(2, 'svc-a', '2026-05-20T22:41:32.200Z', 'second'),
        ],
      },
    })

    expect(buckets[0].cells['svc-a'].entries.map(e => e.message)).toEqual(['first', 'second'])
  })
})
```

- [ ] **Step 2: Run bucket tests and verify they fail**

Run:

```bash
cd desktop
pnpm exec vitest run src/lib/__tests__/searchBuckets.test.ts
```

Expected: FAIL because `../searchBuckets` does not exist.

- [ ] **Step 3: Implement bucket utilities**

Create `desktop/src/lib/searchBuckets.ts`:

```ts
/**
 * 跨服务搜索上下文时间栅格工具。
 *
 * 职责：
 *   - 将各服务日志按固定时间桶对齐
 *   - 为缺日志服务生成空白占位 cell
 *   - 提供稳定数据结构给搜索看板渲染
 *
 * 边界：
 *   - 不读取 DOM，不计算真实像素高度
 *   - 不负责 API 请求或服务隐藏状态
 */
import type { LogEntry } from '@/api/agent'

export interface SearchBucketCell {
  serviceId: string
  entries: LogEntry[]
  blank: boolean
}

export interface SearchBucketRow {
  bucketStart: number
  bucketLabel: string
  cells: Record<string, SearchBucketCell>
}

export interface BuildSearchBucketsInput {
  serviceIds: string[]
  itemsByService: Record<string, LogEntry[]>
  bucketMs?: number
}

function bucketStart(timestamp: string, bucketMs: number): number {
  const time = new Date(timestamp).getTime()
  return Math.floor(time / bucketMs) * bucketMs
}

function bucketLabel(start: number): string {
  return new Date(start).toISOString().slice(11, 19)
}

export function buildSearchBuckets(input: BuildSearchBucketsInput): SearchBucketRow[] {
  const bucketMs = input.bucketMs ?? 1000
  const starts = new Set<number>()
  const grouped: Record<string, Record<number, LogEntry[]>> = {}

  for (const serviceId of input.serviceIds) {
    grouped[serviceId] = {}
    for (const entry of input.itemsByService[serviceId] ?? []) {
      const start = bucketStart(entry.timestamp, bucketMs)
      starts.add(start)
      grouped[serviceId][start] ??= []
      grouped[serviceId][start].push(entry)
    }
  }

  return [...starts].sort((a, b) => a - b).map(start => {
    const cells: Record<string, SearchBucketCell> = {}
    for (const serviceId of input.serviceIds) {
      const entries = grouped[serviceId]?.[start] ?? []
      cells[serviceId] = { serviceId, entries, blank: entries.length === 0 }
    }
    return { bucketStart: start, bucketLabel: bucketLabel(start), cells }
  })
}
```

- [ ] **Step 4: Extend desktop API types**

In `desktop/src/api/agent.ts`, add after `FetchLogsParams`:

```ts
export interface LogSearchResponse {
  query: string
  total: number
  items: LogEntry[]
  service_counts: Record<string, number>
}

export interface LogContextResponse {
  target_id: number
  anchor_time: string
  items_by_service: Record<string, LogEntry[]>
}

export interface SearchLogsParams {
  project: string
  q: string
  service?: string[]
  limit?: number
}

export interface FetchLogContextParams {
  project: string
  id: number
  service?: string[]
  before_ms?: number
  after_ms?: number
}
```

Add to `api` object:

```ts
  searchLogs: (params: SearchLogsParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('q', params.q)
    for (const serviceId of params.service ?? []) qs.append('service', serviceId)
    if (params.limit) qs.set('limit', String(params.limit))
    return request<LogSearchResponse>(`/api/log-search?${qs}`)
  },
  fetchLogContext: (params: FetchLogContextParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('id', String(params.id))
    for (const serviceId of params.service ?? []) qs.append('service', serviceId)
    if (params.before_ms) qs.set('before_ms', String(params.before_ms))
    if (params.after_ms) qs.set('after_ms', String(params.after_ms))
    return request<LogContextResponse>(`/api/logs/context?${qs}`)
  },
```

- [ ] **Step 5: Run desktop utility tests**

Run:

```bash
cd desktop
pnpm exec vitest run src/lib/__tests__/searchBuckets.test.ts
```

Expected: PASS.

- [ ] **Step 6: Run TypeScript check**

Run:

```bash
cd desktop
pnpm exec vue-tsc -b
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

Run:

```bash
git add desktop/src/api/agent.ts desktop/src/lib/searchBuckets.ts desktop/src/lib/__tests__/searchBuckets.test.ts
git commit -m "feat(desktop): add trace search api client"
```

---

## Task 4: Workspace Store And Panel Root Swapping

**Files:**
- Modify: `desktop/src/stores/panel.ts`
- Create: `desktop/src/stores/workspace.ts`
- Create: `desktop/src/stores/__tests__/workspace.test.ts`

- [ ] **Step 1: Add failing workspace tests**

Create `desktop/src/stores/__tests__/workspace.test.ts`:

```ts
/**
 * workspaceStore 测试右侧项目/搜索标签页状态。
 *
 * 职责：
 *   - 验证服务点击复用项目标签
 *   - 验证搜索按钮每次创建新的搜索标签
 *   - 验证隐藏服务和固定服务是搜索标签局部状态
 *
 * 边界：
 *   - 不渲染组件
 *   - 不建立真实 agent 连接
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore } from '../agent'
import { usePanelStore } from '../panel'
import { useWorkspaceStore } from '../workspace'
import type { Project, Service } from '@/api/agent'

function service(id: string, name: string): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

describe('workspaceStore', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('openService 创建项目标签并打开服务', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    workspace.openService('proj-1', 'svc-api')

    expect(workspace.tabs).toHaveLength(1)
    expect(workspace.activeTab?.type).toBe('project')
    expect(panel.allLeaves[0].serviceId).toBe('svc-api')
  })

  it('openService 复用已有项目标签', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore()

    workspace.openService('proj-1', 'svc-api')
    workspace.openService('proj-1', 'svc-worker')

    expect(workspace.tabs.filter(t => t.type === 'project')).toHaveLength(1)
  })

  it('openSearch 每次创建新的搜索标签', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    workspace.openSearch('proj-1')
    workspace.openSearch('proj-1')

    expect(workspace.tabs.filter(t => t.type === 'search')).toHaveLength(2)
    expect(workspace.activeTab?.type).toBe('search')
  })

  it('搜索标签的隐藏和固定服务互不影响', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    const first = workspace.openSearch('proj-1')
    workspace.hideService(first.id, 'svc-api')
    workspace.pinService(first.id, 'svc-api')
    const second = workspace.openSearch('proj-1')

    expect(workspace.searchTab(first.id)?.hiddenServiceIds).toEqual(['svc-api'])
    expect(workspace.searchTab(first.id)?.pinnedServiceIds).toEqual(['svc-api'])
    expect(workspace.searchTab(second.id)?.hiddenServiceIds).toEqual([])
    expect(workspace.searchTab(second.id)?.pinnedServiceIds).toEqual([])
  })
})
```

- [ ] **Step 2: Run workspace tests and verify they fail**

Run:

```bash
cd desktop
pnpm exec vitest run src/stores/__tests__/workspace.test.ts
```

Expected: FAIL because `../workspace` does not exist and panel store lacks root swapping helpers.

- [ ] **Step 3: Add panel root helpers**

In `desktop/src/stores/panel.ts`, export the leaf creation helper:

```ts
export function createEmptyPanelRoot(): PanelLeafNode {
  return makeLeaf()
}
```

Add this action inside `usePanelStore`:

```ts
  function setRoot(nextRoot: PanelNode, nextFocusedPanelId: string | null = null) {
    root.value = nextRoot
    focusedPanelId.value = nextFocusedPanelId
    ensureFocused()
    save()
  }
```

Return it:

```ts
    setRoot,
```

- [ ] **Step 4: Implement workspace store**

Create `desktop/src/stores/workspace.ts`:

```ts
// workspaceStore 管理右侧项目/搜索标签页，是侧边栏和内容区之间的导航状态。
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { api, type LogEntry } from '@/api/agent'
import { useAgentStore } from './agent'
import {
  createEmptyPanelRoot,
  usePanelStore,
  type PanelNode,
} from './panel'

export type WorkspaceTab = ProjectWorkspaceTab | SearchWorkspaceTab

export interface ProjectWorkspaceTab {
  id: string
  type: 'project'
  projectId: string
  title: string
  layoutRoot: PanelNode
  focusedPanelId: string | null
}

export interface SearchWorkspaceTab {
  id: string
  type: 'search'
  projectId: string
  title: string
  query: string
  status: 'empty' | 'loading' | 'results' | 'emptyResults' | 'error'
  results: LogEntry[]
  serviceCounts: Record<string, number>
  hiddenServiceIds: string[]
  selectedLogId: number | null
  contextAnchorTime: string | null
  contextByService: Record<string, LogEntry[]>
  pinnedServiceIds: string[]
  error: string | null
}

function makeProjectTab(projectId: string, title: string): ProjectWorkspaceTab {
  return {
    id: uuidv4(),
    type: 'project',
    projectId,
    title,
    layoutRoot: createEmptyPanelRoot(),
    focusedPanelId: null,
  }
}

function makeSearchTab(projectId: string, title: string): SearchWorkspaceTab {
  return {
    id: uuidv4(),
    type: 'search',
    projectId,
    title,
    query: '',
    status: 'empty',
    results: [],
    serviceCounts: {},
    hiddenServiceIds: [],
    selectedLogId: null,
    contextAnchorTime: null,
    contextByService: {},
    pinnedServiceIds: [],
    error: null,
  }
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const tabs = ref<WorkspaceTab[]>([])
  const activeTabId = ref<string | null>(null)

  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value) ?? null)

  function projectName(projectId: string): string {
    return useAgentStore().projectById(projectId)?.name ?? projectId
  }

  function saveActiveProjectLayout() {
    const active = activeTab.value
    if (!active || active.type !== 'project') return
    const panel = usePanelStore()
    active.layoutRoot = panel.root
    active.focusedPanelId = panel.focusedPanelId
  }

  function activateTab(tabId: string) {
    saveActiveProjectLayout()
    activeTabId.value = tabId
    const tab = activeTab.value
    if (tab?.type === 'project') {
      usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    }
  }

  function ensureProjectTab(projectId: string): ProjectWorkspaceTab {
    const existing = tabs.value.find(
      (tab): tab is ProjectWorkspaceTab => tab.type === 'project' && tab.projectId === projectId,
    )
    if (existing) return existing
    const tab = makeProjectTab(projectId, projectName(projectId))
    tabs.value.push(tab)
    return tab
  }

  function openService(projectId: string, serviceId: string) {
    const tab = ensureProjectTab(projectId)
    activateTab(tab.id)
    const panel = usePanelStore()
    const existing = panel.allLeaves.find(leaf => leaf.serviceId === serviceId)
    const targetPanelId = existing?.id ?? panel.targetPanelId()
    if (!targetPanelId) return
    panel.replaceScope(targetPanelId, serviceId, projectId)
    panel.setFocus(targetPanelId)
    saveActiveProjectLayout()
  }

  function openSearch(projectId: string): SearchWorkspaceTab {
    saveActiveProjectLayout()
    const tab = makeSearchTab(projectId, `Search · ${projectName(projectId)}`)
    tabs.value.push(tab)
    activeTabId.value = tab.id
    return tab
  }

  function searchTab(tabId: string): SearchWorkspaceTab | null {
    const tab = tabs.value.find(t => t.id === tabId)
    return tab?.type === 'search' ? tab : null
  }

  function hideService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab || tab.hiddenServiceIds.includes(serviceId)) return
    tab.hiddenServiceIds.push(serviceId)
  }

  function showService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab) return
    tab.hiddenServiceIds = tab.hiddenServiceIds.filter(id => id !== serviceId)
  }

  function pinService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab || tab.pinnedServiceIds.includes(serviceId)) return
    tab.pinnedServiceIds.push(serviceId)
  }

  function unpinService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab) return
    tab.pinnedServiceIds = tab.pinnedServiceIds.filter(id => id !== serviceId)
  }

  async function runSearch(tabId: string, query: string) {
    const tab = searchTab(tabId)
    const trimmed = query.trim()
    if (!tab || !trimmed) return
    tab.query = trimmed
    tab.title = `Search: ${trimmed}`
    tab.status = 'loading'
    tab.error = null
    try {
      const result = await api.searchLogs({ project: tab.projectId, q: trimmed })
      tab.results = result.items
      tab.serviceCounts = result.service_counts
      tab.status = result.items.length ? 'results' : 'emptyResults'
    } catch (err) {
      tab.error = err instanceof Error ? err.message : String(err)
      tab.status = 'error'
    }
  }

  async function loadContext(tabId: string, logId: number) {
    const tab = searchTab(tabId)
    if (!tab) return
    const visibleServices = Object.keys(tab.serviceCounts).filter(
      serviceId => !tab.hiddenServiceIds.includes(serviceId),
    )
    const result = await api.fetchLogContext({
      project: tab.projectId,
      id: logId,
      service: visibleServices,
    })
    tab.selectedLogId = result.target_id
    tab.contextAnchorTime = result.anchor_time
    for (const serviceId of visibleServices) {
      if (tab.pinnedServiceIds.includes(serviceId)) continue
      tab.contextByService[serviceId] = result.items_by_service[serviceId] ?? []
    }
  }

  function closeTab(tabId: string) {
    const idx = tabs.value.findIndex(t => t.id === tabId)
    if (idx < 0) return
    tabs.value.splice(idx, 1)
    if (activeTabId.value !== tabId) return
    activeTabId.value = tabs.value[Math.max(0, idx - 1)]?.id ?? null
    const tab = activeTab.value
    if (tab?.type === 'project') {
      usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    }
  }

  return {
    tabs,
    activeTabId,
    activeTab,
    activateTab,
    openService,
    openSearch,
    searchTab,
    hideService,
    showService,
    pinService,
    unpinService,
    runSearch,
    loadContext,
    closeTab,
    saveActiveProjectLayout,
  }
})
```

- [ ] **Step 5: Run workspace tests**

Run:

```bash
cd desktop
pnpm exec vitest run src/stores/__tests__/workspace.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

Run:

```bash
git add desktop/src/stores/panel.ts desktop/src/stores/workspace.ts desktop/src/stores/__tests__/workspace.test.ts
git commit -m "feat(desktop): add workspace tab store"
```

---

## Task 5: Workspace Shell And Sidebar Entry Points

**Files:**
- Create: `desktop/src/components/Workspace/WorkspaceShell.vue`
- Create: `desktop/src/components/Workspace/WorkspaceTabs.vue`
- Create: `desktop/src/components/Search/SearchPage.vue`
- Modify: `desktop/src/pages/MainPage.vue`
- Modify: `desktop/src/components/Sidebar/SidebarView.vue`
- Modify: `desktop/src/components/Sidebar/ProjectHeader.vue`

- [ ] **Step 1: Create workspace tabs component**

Create `desktop/src/components/Workspace/WorkspaceTabs.vue`:

```vue
<!--
工作区标签栏组件

职责：
  - 渲染项目标签和搜索标签
  - 切换/关闭 workspace tab

边界：
  - 不渲染标签内容
  - 不负责创建标签，创建由侧边栏触发
-->
<script setup lang="ts">
import { useWorkspaceStore } from '@/stores/workspace'

const workspace = useWorkspaceStore()
</script>

<template>
  <div class="workspace-tabs">
    <button
      v-for="tab in workspace.tabs"
      :key="tab.id"
      class="workspace-tab"
      :class="{ active: workspace.activeTabId === tab.id, search: tab.type === 'search' }"
      @click="workspace.activateTab(tab.id)"
    >
      <span class="tab-kind">{{ tab.type === 'project' ? '▣' : '⌕' }}</span>
      <span class="tab-title">{{ tab.title }}</span>
      <span class="tab-close" @click.stop="workspace.closeTab(tab.id)">×</span>
    </button>
  </div>
</template>

<style scoped>
.workspace-tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding: 3px 8px 0;
  background: var(--bg-primary);
  border-bottom: 1px solid var(--border-secondary);
  overflow-x: auto;
  flex-shrink: 0;
}
.workspace-tab {
  display: flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  max-width: 220px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-bottom: none;
  border-radius: 5px 5px 0 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}
.workspace-tab.active {
  background: var(--bg-elevated);
  border-color: var(--border-secondary);
  color: var(--text-primary);
}
.workspace-tab.search .tab-kind { color: #58a6ff; }
.tab-title {
  overflow: hidden;
  text-overflow: ellipsis;
}
.tab-close {
  color: var(--text-tertiary);
  padding: 0 2px;
}
.tab-close:hover { color: var(--text-primary); }
</style>
```

- [ ] **Step 2: Create workspace shell**

Create `desktop/src/components/Workspace/WorkspaceShell.vue`:

```vue
<!--
工作区壳组件

职责：
  - 在右侧主内容区顶部提供工作区标签栏
  - 根据 active tab 渲染项目日志面板或搜索页

边界：
  - 不直接处理侧边栏点击
  - 不实现搜索结果渲染细节
-->
<script setup lang="ts">
import PanelLayout from '@/components/Panel/PanelLayout.vue'
import SearchPage from '@/components/Search/SearchPage.vue'
import WorkspaceTabs from './WorkspaceTabs.vue'
import { useWorkspaceStore } from '@/stores/workspace'

const workspace = useWorkspaceStore()
</script>

<template>
  <div class="workspace-shell">
    <WorkspaceTabs v-if="workspace.tabs.length" />
    <div v-if="!workspace.activeTab" class="workspace-empty">
      <div>选择左侧服务或点击项目搜索</div>
    </div>
    <PanelLayout v-else-if="workspace.activeTab.type === 'project'" />
    <SearchPage
      v-else
      :tab-id="workspace.activeTab.id"
    />
  </div>
</template>

<style scoped>
.workspace-shell {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.workspace-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
```

- [ ] **Step 3: Replace MainPage content**

In `desktop/src/pages/MainPage.vue`, replace `PanelLayout` import with:

```ts
import WorkspaceShell from '@/components/Workspace/WorkspaceShell.vue'
```

Replace `<PanelLayout />` with:

```vue
<WorkspaceShell />
```

- [ ] **Step 4: Create minimal SearchPage**

Create `desktop/src/components/Search/SearchPage.vue`:

```vue
<!--
搜索标签页组件

职责：
  - 作为搜索标签页内容入口
  - 在搜索功能接入前显示项目级搜索页面骨架

边界：
  - 不执行搜索请求
  - 不渲染搜索结果看板
-->
<script setup lang="ts">
defineProps<{ tabId: string }>()
</script>

<template>
  <div class="search-page">
    <div class="search-empty">输入关键词搜索当前项目历史日志</div>
  </div>
</template>

<style scoped>
.search-page {
  display: flex;
  flex: 1;
  min-height: 0;
  background: var(--bg-primary);
}
.search-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
```

- [ ] **Step 5: Update sidebar service clicks**

In `desktop/src/components/Sidebar/SidebarView.vue`, keep `usePanelStore` for selected-row highlighting, remove the old `focusedLeaf` computed, and add:

```ts
import { usePanelStore } from '@/stores/panel'
import { useWorkspaceStore } from '@/stores/workspace'

const panelStore = usePanelStore()
const workspace = useWorkspaceStore()

function isServiceSelected(serviceId: string) {
  const active = workspace.activeTab
  if (!active || active.type !== 'project') return false
  return panelStore.allLeaves.some(leaf => leaf.serviceId === serviceId)
}

function selectService(serviceId: string, projectId: string) {
  workspace.openService(projectId, serviceId)
}

function openProjectSearch(projectId: string) {
  workspace.openSearch(projectId)
}
```

Update `ProjectHeader` usage:

```vue
<ProjectHeader :project="project" @search="openProjectSearch(project.id)" />
```

- [ ] **Step 6: Add project search button**

In `desktop/src/components/Sidebar/ProjectHeader.vue`, add emit:

```ts
const emit = defineEmits<{
  search: []
}>()
```

Add button in `.project-actions` before stop:

```vue
<button
  title="搜索项目日志"
  class="action-btn search"
  :disabled="project.services.length === 0"
  @click.stop="emit('search')"
>⌕</button>
```

Add style:

```css
.action-btn.search { color: #58a6ff; }
```

- [ ] **Step 7: Run type check**

Run:

```bash
cd desktop
pnpm exec vue-tsc -b
```

Expected: PASS.

- [ ] **Step 8: Commit Task 5**

Run:

```bash
git add desktop/src/components/Workspace desktop/src/components/Search/SearchPage.vue desktop/src/pages/MainPage.vue desktop/src/components/Sidebar/SidebarView.vue desktop/src/components/Sidebar/ProjectHeader.vue
git commit -m "feat(desktop): add project workspace tabs"
```

---

## Task 6: Search Page Empty/Loading/Results State

**Files:**
- Modify: `desktop/src/components/Search/SearchPage.vue`
- Create: `desktop/src/components/Search/SearchBoard.vue`

- [ ] **Step 1: Create SearchBoard stub**

Create `desktop/src/components/Search/SearchBoard.vue`:

```vue
<!--
跨服务搜索结果看板

职责：
  - 承载搜索结果的左侧导航和右侧服务分栏

边界：
  - 当前任务先提供可编译容器，详细分栏在后续任务实现
-->
<script setup lang="ts">
defineProps<{ tabId: string }>()
</script>

<template>
  <div class="search-board">
    <slot />
  </div>
</template>

<style scoped>
.search-board {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
```

- [ ] **Step 2: Replace SearchPage with searchable state UI**

Create `desktop/src/components/Search/SearchPage.vue`:

```vue
<!--
搜索标签页组件

职责：
  - 提供项目级历史日志搜索入口
  - 渲染搜索状态：空、加载、结果、无结果、失败

边界：
  - 不直接访问 agent API，通过 workspaceStore 执行搜索
  - 不实现右侧分栏细节，交给 SearchBoard
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import SearchBoard from './SearchBoard.vue'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const input = ref('')

const tab = computed(() => workspace.searchTab(props.tabId))
const project = computed(() => tab.value ? agentStore.projectById(tab.value.projectId) : null)

watch(tab, value => {
  input.value = value?.query ?? ''
}, { immediate: true })

function submit() {
  if (!tab.value) return
  void workspace.runSearch(tab.value.id, input.value)
}
</script>

<template>
  <div v-if="tab" class="search-page">
    <div class="search-top">
      <div class="project-name">{{ project?.name ?? tab.projectId }}</div>
      <form class="search-form" @submit.prevent="submit">
        <input
          v-model="input"
          class="search-input"
          placeholder="输入 traceID、orderID、错误关键字..."
          autofocus
        />
        <button class="search-button" :disabled="tab.status === 'loading'">搜索</button>
      </form>
      <div v-if="tab.status === 'results'" class="result-summary">
        {{ tab.results.length }} / {{ Object.values(tab.serviceCounts).reduce((a, b) => a + b, 0) }} 条命中
      </div>
    </div>

    <div v-if="tab.status === 'empty'" class="search-empty">
      <div class="search-brand">Trace Search</div>
    </div>
    <div v-else-if="tab.status === 'loading'" class="search-state">搜索中...</div>
    <div v-else-if="tab.status === 'emptyResults'" class="search-state">当前项目没有匹配日志</div>
    <div v-else-if="tab.status === 'error'" class="search-state error">{{ tab.error }}</div>
    <SearchBoard v-else :tab-id="tab.id" />
  </div>
</template>

<style scoped>
.search-page {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  background: var(--bg-primary);
}
.search-top {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  flex-shrink: 0;
}
.project-name {
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.search-form {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
}
.search-input {
  flex: 1;
  min-width: 180px;
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 6px 9px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 12px;
  outline: none;
}
.search-button {
  border: 1px solid rgba(88, 166, 255, 0.35);
  border-radius: 5px;
  background: rgba(88, 166, 255, 0.12);
  color: #58a6ff;
  padding: 6px 12px;
  font-size: 12px;
  cursor: pointer;
}
.search-button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.result-summary {
  color: var(--text-tertiary);
  font-size: 11px;
}
.search-empty,
.search-state {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 13px;
}
.search-brand {
  font-size: 22px;
  color: var(--text-secondary);
}
.search-state.error {
  color: #f85149;
}
</style>
```

- [ ] **Step 3: Run type check**

Run:

```bash
cd desktop
pnpm exec vue-tsc -b
```

Expected: PASS.

- [ ] **Step 4: Run workspace tests**

Run:

```bash
cd desktop
pnpm exec vitest run src/stores/__tests__/workspace.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit Task 6**

Run:

```bash
git add desktop/src/components/Search/SearchPage.vue desktop/src/components/Search/SearchBoard.vue
git commit -m "feat(desktop): add trace search page states"
```

---

## Task 7: Search Board Left Rail And Timeline

**Files:**
- Create: `desktop/src/components/Search/SearchServiceRail.vue`
- Create: `desktop/src/components/Search/SearchTimeline.vue`
- Modify: `desktop/src/components/Search/SearchBoard.vue`

- [ ] **Step 1: Create service rail**

Create `desktop/src/components/Search/SearchServiceRail.vue`:

```vue
<!--
搜索命中服务栏

职责：
  - 显示命中服务和命中数量
  - 控制当前搜索标签内的服务隐藏/显示

边界：
  - 不修改项目级过滤规则
  - 不负责加载上下文
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))

const rows = computed(() => {
  if (!tab.value) return []
  return Object.entries(tab.value.serviceCounts)
    .sort((a, b) => b[1] - a[1])
    .map(([serviceId, count], index) => ({
      serviceId,
      count,
      service: agentStore.serviceById(serviceId),
      color: serviceColor(index),
      hidden: tab.value!.hiddenServiceIds.includes(serviceId),
    }))
})

function serviceColor(index: number): string {
  const colors = ['#58a6ff', '#f2cc60', '#56d364', '#ff7b72', '#d2a8ff', '#79c0ff']
  return colors[index % colors.length]
}

function toggle(serviceId: string, hidden: boolean) {
  if (!tab.value) return
  if (hidden) workspace.showService(tab.value.id, serviceId)
  else workspace.hideService(tab.value.id, serviceId)
}
</script>

<template>
  <div class="service-rail">
    <div class="rail-title">命中服务</div>
    <button
      v-for="row in rows"
      :key="row.serviceId"
      class="service-hit"
      :class="{ hidden: row.hidden }"
      @click="toggle(row.serviceId, row.hidden)"
    >
      <span class="dot" :style="{ backgroundColor: row.color }" />
      <span class="name">{{ row.service?.name ?? row.serviceId }}</span>
      <span class="count">{{ row.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.service-rail {
  max-height: 33%;
  min-height: 96px;
  overflow-y: auto;
  padding: 8px;
  border-bottom: 1px solid var(--border-secondary);
}
.rail-title {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  margin-bottom: 6px;
}
.service-hit {
  display: grid;
  grid-template-columns: 10px 1fr auto;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  padding: 4px 5px;
  font-size: 11px;
  cursor: pointer;
}
.service-hit:hover { background: var(--bg-overlay); }
.service-hit.hidden { opacity: 0.45; text-decoration: line-through; }
.dot { width: 7px; height: 7px; border-radius: 999px; }
.name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; }
.count { color: var(--text-tertiary); }
</style>
```

- [ ] **Step 2: Create timeline**

Create `desktop/src/components/Search/SearchTimeline.vue`:

```vue
<!--
搜索命中时间线

职责：
  - 展示匹配关键词的日志列表
  - 点击日志后请求跨服务上下文

边界：
  - 只展示命中日志，不展示上下文日志
  - 不负责右侧分栏渲染
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))

const visibleResults = computed(() => {
  if (!tab.value) return []
  const hidden = new Set(tab.value.hiddenServiceIds)
  return tab.value.results.filter(entry => !hidden.has(entry.service_id))
})

function timeLabel(timestamp: string): string {
  return new Date(timestamp).toISOString().slice(11, 23)
}

function serviceName(serviceId: string): string {
  return agentStore.serviceById(serviceId)?.name ?? serviceId
}

function select(entryId: number) {
  if (!tab.value) return
  void workspace.loadContext(tab.value.id, entryId)
}
</script>

<template>
  <div class="timeline">
    <button
      v-for="entry in visibleResults"
      :key="entry.id"
      class="timeline-row"
      :class="{ selected: tab?.selectedLogId === entry.id }"
      @click="select(entry.id)"
    >
      <span class="time">{{ timeLabel(entry.timestamp) }}</span>
      <span class="service">{{ serviceName(entry.service_id) }}</span>
      <span class="message">{{ entry.message }}</span>
    </button>
  </div>
</template>

<style scoped>
.timeline {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px;
}
.timeline-row {
  display: grid;
  grid-template-columns: 78px 72px 1fr;
  gap: 6px;
  width: 100%;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  padding: 4px 5px;
  font-size: 11px;
  text-align: left;
  cursor: pointer;
}
.timeline-row:hover { background: var(--bg-overlay); }
.timeline-row.selected { background: rgba(88, 166, 255, 0.14); }
.time { color: var(--text-tertiary); font-variant-numeric: tabular-nums; }
.service { color: #58a6ff; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.message { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
```

- [ ] **Step 3: Update SearchBoard to use left rail**

Replace `desktop/src/components/Search/SearchBoard.vue` with:

```vue
<!--
跨服务搜索结果看板

职责：
  - 左侧展示命中服务和命中时间线
  - 右侧展示按服务分栏的上下文

边界：
  - 不执行搜索请求
  - 不修改项目级过滤规则
-->
<script setup lang="ts">
import SearchServiceRail from './SearchServiceRail.vue'
import SearchTimeline from './SearchTimeline.vue'

defineProps<{ tabId: string }>()
</script>

<template>
  <div class="search-board">
    <aside class="search-left">
      <SearchServiceRail :tab-id="tabId" />
      <SearchTimeline :tab-id="tabId" />
    </aside>
    <section class="search-right">
      <div class="context-empty">点击左侧命中日志查看跨服务上下文</div>
    </section>
  </div>
</template>

<style scoped>
.search-board {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.search-left {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-right: 1px solid var(--border-secondary);
  background: var(--bg-primary);
}
.search-right {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--bg-primary);
}
.context-empty {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
```

- [ ] **Step 4: Run type check**

Run:

```bash
cd desktop
pnpm exec vue-tsc -b
```

Expected: PASS.

- [ ] **Step 5: Commit Task 7**

Run:

```bash
git add desktop/src/components/Search/SearchBoard.vue desktop/src/components/Search/SearchServiceRail.vue desktop/src/components/Search/SearchTimeline.vue
git commit -m "feat(desktop): add trace search result navigation"
```

---

## Task 8: Right-Side Synchronized Service Columns

**Files:**
- Create: `desktop/src/components/Search/SearchServiceColumns.vue`
- Modify: `desktop/src/components/Search/SearchBoard.vue`

- [ ] **Step 1: Create service columns component**

Create `desktop/src/components/Search/SearchServiceColumns.vue`:

```vue
<!--
搜索上下文服务分栏

职责：
  - 按服务列展示同一时间栅格的上下文日志
  - 支持服务列固定/取消固定
  - 高亮搜索目标日志

边界：
  - 不负责构造时间栅格，使用 searchBuckets 工具
  - 不执行上下文 API 请求
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { buildSearchBuckets } from '@/lib/searchBuckets'
import type { LogEntry } from '@/api/agent'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))

const visibleServiceIds = computed(() => {
  if (!tab.value) return []
  return Object.keys(tab.value.serviceCounts).filter(
    serviceId => !tab.value!.hiddenServiceIds.includes(serviceId),
  )
})

const buckets = computed(() => {
  if (!tab.value) return []
  return buildSearchBuckets({
    serviceIds: visibleServiceIds.value,
    itemsByService: tab.value.contextByService,
  })
})

function serviceName(serviceId: string): string {
  return agentStore.serviceById(serviceId)?.name ?? serviceId
}

function timeLabel(entry: LogEntry): string {
  return new Date(entry.timestamp).toISOString().slice(11, 23)
}

function togglePin(serviceId: string) {
  if (!tab.value) return
  if (tab.value.pinnedServiceIds.includes(serviceId)) {
    workspace.unpinService(tab.value.id, serviceId)
  } else {
    workspace.pinService(tab.value.id, serviceId)
  }
}
</script>

<template>
  <div v-if="tab?.contextAnchorTime" class="columns">
    <div
      v-for="serviceId in visibleServiceIds"
      :key="serviceId"
      class="service-column"
    >
      <div class="column-header">
        <span class="service-name">{{ serviceName(serviceId) }}</span>
        <button class="pin-btn" @click="togglePin(serviceId)">
          {{ tab.pinnedServiceIds.includes(serviceId) ? '已固定' : '固定' }}
        </button>
      </div>
      <div class="column-body">
        <div
          v-for="bucket in buckets"
          :key="bucket.bucketStart"
          class="bucket-row"
        >
          <div class="bucket-time">{{ bucket.bucketLabel }}</div>
          <div v-if="bucket.cells[serviceId].blank" class="blank-cell" />
          <div v-else class="entry-stack">
            <div
              v-for="entry in bucket.cells[serviceId].entries"
              :key="entry.id"
              class="context-entry"
              :class="{ target: entry.id === tab.selectedLogId }"
            >
              <span class="entry-time">{{ timeLabel(entry) }}</span>
              <span class="entry-level">{{ entry.level }}</span>
              <span class="entry-message">{{ entry.message }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
  <div v-else class="columns-empty">
    点击左侧命中日志查看跨服务上下文
  </div>
</template>

<style scoped>
.columns {
  display: flex;
  min-width: 0;
  height: 100%;
  overflow-x: auto;
  overflow-y: hidden;
}
.service-column {
  display: flex;
  flex-direction: column;
  width: 360px;
  min-width: 300px;
  border-right: 1px solid var(--border-secondary);
  min-height: 0;
}
.column-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 32px;
  padding: 0 8px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--bg-elevated);
  flex-shrink: 0;
}
.service-name {
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 600;
}
.pin-btn {
  border: 1px solid var(--border);
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 10px;
  padding: 2px 6px;
  cursor: pointer;
}
.column-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}
.bucket-row {
  min-height: 28px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
  padding: 3px 6px;
}
.bucket-time {
  color: var(--text-tertiary);
  font-size: 9px;
  font-variant-numeric: tabular-nums;
  margin-bottom: 2px;
}
.blank-cell {
  min-height: 20px;
}
.entry-stack {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.context-entry {
  display: grid;
  grid-template-columns: 74px 48px 1fr;
  gap: 5px;
  border-radius: 3px;
  padding: 2px 4px;
  color: var(--text-secondary);
  font-size: 10px;
  line-height: 1.45;
}
.context-entry.target {
  background: rgba(88, 166, 255, 0.18);
  outline: 1px solid rgba(88, 166, 255, 0.35);
}
.entry-time,
.entry-level {
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}
.entry-message {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.columns-empty {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
```

- [ ] **Step 2: Wire columns into board**

In `desktop/src/components/Search/SearchBoard.vue`, import:

```ts
import SearchServiceColumns from './SearchServiceColumns.vue'
```

Replace the `.search-right` content:

```vue
<section class="search-right">
  <SearchServiceColumns :tab-id="tabId" />
</section>
```

- [ ] **Step 3: Run bucket and type checks**

Run:

```bash
cd desktop
pnpm exec vitest run src/lib/__tests__/searchBuckets.test.ts
pnpm exec vue-tsc -b
```

Expected: both PASS.

- [ ] **Step 4: Commit Task 8**

Run:

```bash
git add desktop/src/components/Search/SearchBoard.vue desktop/src/components/Search/SearchServiceColumns.vue
git commit -m "feat(desktop): add synchronized trace columns"
```

---

## Task 9: Full Verification And Build

**Files:**
- No new files.

- [ ] **Step 1: Run all agent tests**

Run:

```bash
cd agent
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run all desktop tests**

Run:

```bash
cd desktop
pnpm exec vitest run
```

Expected: PASS.

- [ ] **Step 3: Run desktop build**

Run:

```bash
cd desktop
pnpm build
```

Expected: PASS.

- [ ] **Step 4: Manual behavior checklist**

Run the app using the existing project workflow. Do not add new behavior while checking.

Checklist:

- [ ] Sidebar project header shows a search button.
- [ ] Clicking a service opens or reuses its project tab.
- [ ] Clicking project search twice creates two search tabs.
- [ ] Searching a traceID shows service counts in the left rail.
- [ ] Hiding a service removes it from the timeline and right columns.
- [ ] Clicking a timeline result loads right-side context columns.
- [ ] Fixed service column does not change when another timeline result is clicked.
- [ ] Unfixed service columns change to the new selected time.

- [ ] **Step 5: Commit verification fixes only if needed**

If any verification step required code fixes:

```bash
git add <fixed-files>
git commit -m "fix: stabilize trace search workflow"
```

If no fixes were needed, do not create an empty commit.

---

## Self-Review Checklist

- Spec coverage:
  - Project tabs: Task 4 and Task 5.
  - Sidebar service click and search button: Task 5.
  - Search tab every click creates a new tab: Task 4 and Task 5.
  - Project-wide historical search: Task 1, Task 2, Task 3, Task 6.
  - Left service counts and timeline: Task 7.
  - Right synchronized service columns: Task 8.
  - Hidden services: Task 4 and Task 7.
  - Pinned service columns: Task 4 and Task 8.
  - Time-grid blank behavior: Task 3 and Task 8.
  - Context API: Task 1 and Task 2.
- Placeholder scan:
  - Completed. The plan contains no red-flag placeholders or unspecified tests.
- Type consistency:
  - `items_by_service` in API maps to `contextByService` in store.
  - `service_counts` in API maps to `serviceCounts` in store.
  - `SearchWorkspaceTab` fields match component usage.
