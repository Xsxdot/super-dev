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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

// recentLogBase 返回种子日志的基准时间（1 分钟前，整秒）。
//
// 必须使用相对当前时间的时间戳：NewApp 会启动后台清理 goroutine，
// 启动后立即按保留期（默认 7 天）执行 DeleteOlderThan。若种子日志用
// 硬编码历史日期，一旦日历跨过保留边界，清理任务就会与测试断言竞争，
// 把刚插入的日志异步删掉（慢机器上随机失败）。取整秒是为了保证
// 时间戳经 SQLite 与 JSON 往返后仍可与基准时间精确相等比较。
func recentLogBase() time.Time {
	return time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
}

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
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "trace-8f21 api", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-8f21 worker", Stream: "stdout"},
		{DeploymentID: "other", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "trace-8f21 outside", Stream: "stdout"},
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
	assert.Equal(t, "svc-a", body.Items[0].DeploymentID)
	assert.Equal(t, "svc-b", body.Items[1].DeploymentID)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, body.DeploymentCounts)
}

// TestLogSearchAPIUsesDeploymentIDs 验证项目级搜索以 deployment ID 作为日志归属范围。
func TestLogSearchAPIUsesDeploymentIDs(t *testing.T) {
	app, srv := newSearchTestServer(t)
	app.projects[0].Services = []model.Service{
		{
			ID:        "svc-api",
			ProjectID: "proj-1",
			Name:      "api",
			Deployments: []model.Deployment{
				{ID: "dep-api", EnvName: "dev", Location: model.LocationLocal},
			},
		},
		{
			ID:        "svc-worker",
			ProjectID: "proj-1",
			Name:      "worker",
			Deployments: []model.Deployment{
				{ID: "dep-worker", EnvName: "dev", Location: model.LocationLocal},
			},
		},
	}
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "dep-api", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "trace-dep api", Stream: "stdout"},
		{DeploymentID: "dep-worker", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-dep worker", Stream: "stdout"},
		{DeploymentID: "outside", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "trace-dep outside", Stream: "stdout"},
	}))

	resp, err := http.Get(srv.URL + "/api/log-search?project=proj-1&q=trace-dep")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logSearchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, 2, body.Total)
	require.Len(t, body.Items, 2)
	assert.Equal(t, "dep-api", body.Items[0].DeploymentID)
	assert.Equal(t, "dep-worker", body.Items[1].DeploymentID)
	assert.Equal(t, map[string]int{"dep-api": 1, "dep-worker": 1}, body.DeploymentCounts)
}

func TestLogSearchAPIUsesDeploymentBackends(t *testing.T) {
	app, srv := newSearchTestServer(t)
	app.projects[0].Services = []model.Service{
		{
			ID:        "svc-api",
			ProjectID: "proj-1",
			Name:      "api",
			Deployments: []model.Deployment{
				{ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"host-1"}},
			},
		},
	}
	base := recentLogBase()
	backend := &searchLogBackend{
		entries: []model.LogEntry{
			{ID: 77, DeploymentID: "dep-remote", RunID: "run-remote", Timestamp: base, Level: "INFO", Message: "remote needle", Stream: "stdout"},
		},
	}
	app.SetBackendForTest("dep-remote", backend)

	resp, err := http.Get(srv.URL + "/api/log-search?project=proj-1&q=needle")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logSearchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, logbackend.SearchQuery{
		Text:          "needle",
		DeploymentIDs: []string{"dep-remote"},
		Limit:         defaultSearchLimit,
	}, backend.query)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "dep-remote", body.Items[0].DeploymentID)
	assert.Equal(t, map[string]int{"dep-remote": 1}, body.DeploymentCounts)
}

func TestLogSearchAPIPagesAfterCursor(t *testing.T) {
	app, srv := newSearchTestServer(t)
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "trace page api", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace page worker", Stream: "stdout"},
	}))

	firstURL := srv.URL + "/api/log-search?project=proj-1&q=trace+page&limit=1"
	firstResp, err := http.Get(firstURL)
	require.NoError(t, err)
	defer firstResp.Body.Close()
	require.Equal(t, http.StatusOK, firstResp.StatusCode)
	var first logSearchResponse
	require.NoError(t, json.NewDecoder(firstResp.Body).Decode(&first))
	require.Len(t, first.Items, 1)
	assert.True(t, first.HasMore)

	query := url.Values{}
	query.Set("project", "proj-1")
	query.Set("q", "trace page")
	query.Set("limit", "1")
	query.Set("cursor_time", first.Items[0].Timestamp.Format(time.RFC3339Nano))
	query.Set("cursor_id", strconv.FormatInt(first.Items[0].ID, 10))
	secondResp, err := http.Get(srv.URL + "/api/log-search?" + query.Encode())
	require.NoError(t, err)
	defer secondResp.Body.Close()
	require.Equal(t, http.StatusOK, secondResp.StatusCode)

	var second logSearchResponse
	require.NoError(t, json.NewDecoder(secondResp.Body).Decode(&second))
	require.Len(t, second.Items, 1)
	assert.Equal(t, "svc-b", second.Items[0].DeploymentID)
	assert.False(t, second.HasMore)
	assert.Equal(t, 2, second.Total)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, second.DeploymentCounts)
}

func TestLogSearchAPIAllowsServiceWithoutProject(t *testing.T) {
	app, srv := newSearchTestServer(t)
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "collector-1", RunID: "run-collector", Timestamp: base, Level: "INFO", Message: "remote collector trace", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "remote collector trace", Stream: "stdout"},
	}))

	resp, err := http.Get(srv.URL + "/api/log-search?deployment=collector-1&query=collector")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logSearchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, "collector-1", body.Items[0].DeploymentID)
	assert.Equal(t, map[string]int{"collector-1": 1}, body.DeploymentCounts)
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
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "trace-8f21 target", Stream: "stderr"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "worker context", Stream: "stdout"},
	}))
	search, err := app.store.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/api/logs/context?project=proj-1&id=" + strconv.FormatInt(search.Entries[0].ID, 10))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logContextResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, search.Entries[0].ID, body.TargetID)
	assert.Equal(t, base, body.AnchorTime)
	assert.Len(t, body.ItemsByDeployment["svc-a"], 1)
	assert.Len(t, body.ItemsByDeployment["svc-b"], 1)
	assert.Len(t, body.ItemsByDeployment["svc-c"], 0)
}

func TestLogContextAPIAllowsDeploymentWithoutProject(t *testing.T) {
	app, srv := newSearchTestServer(t)
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "collector-1", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "collector target", Stream: "stderr"},
		{DeploymentID: "collector-1", RunID: "run-1", Timestamp: base.Add(100 * time.Millisecond), Level: "INFO", Message: "collector after", Stream: "stdout"},
	}))
	search, err := app.store.Search(store.SearchParams{DeploymentIDs: []string{"collector-1"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/api/logs/context?deployment=collector-1&id=" + strconv.FormatInt(search.Entries[0].ID, 10))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logContextResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, search.Entries[0].ID, body.TargetID)
	assert.Len(t, body.ItemsByDeployment["collector-1"], 2)
}

func TestLogContextAPIUsesDeploymentBackendWhenStoreMisses(t *testing.T) {
	app, srv := newSearchTestServer(t)
	app.projects[0].Services = []model.Service{
		{
			ID:        "svc-api",
			ProjectID: "proj-1",
			Name:      "api",
			Deployments: []model.Deployment{
				{ID: "dep-remote", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"host-1"}},
			},
		},
	}
	base := recentLogBase()
	backend := &searchLogBackend{
		contextResult: logbackend.ContextResult{
			TargetID:   77,
			AnchorTime: base,
			Items: []model.LogEntry{
				{ID: 76, DeploymentID: "collector-internal", Timestamp: base.Add(-time.Second), Level: "INFO", Message: "before", Stream: "stdout"},
				{ID: 77, DeploymentID: "collector-internal", Timestamp: base, Level: "ERROR", Message: "target", Stream: "stderr"},
			},
		},
	}
	app.SetBackendForTest("dep-remote", backend)

	resp, err := http.Get(srv.URL + "/api/logs/context?project=proj-1&deployment=dep-remote&id=77&before_ms=1000&after_ms=1000")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logContextResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, int64(77), body.TargetID)
	assert.Equal(t, base, body.AnchorTime)
	assert.Equal(t, logbackend.ContextQuery{
		TargetID:     77,
		DeploymentID: "dep-remote",
		Before:       time.Second,
		After:        time.Second,
	}, backend.contextQuery)
	require.Len(t, body.ItemsByDeployment["dep-remote"], 2)
	assert.Equal(t, "dep-remote", body.ItemsByDeployment["dep-remote"][0].DeploymentID)
}

func TestLogContextPageAPI(t *testing.T) {
	app, srv := newSearchTestServer(t)
	base := recentLogBase()
	require.NoError(t, app.store.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "api older", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-time.Second), Level: "INFO", Message: "api near", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "target", Stream: "stderr"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(-time.Second), Level: "INFO", Message: "worker near", Stream: "stdout"},
	}))
	search, err := app.store.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	target := search.Entries[0]
	query := url.Values{}
	query.Set("project", "proj-1")
	query.Set("deployment", "svc-a")
	query.Set("direction", string(store.ContextPageBefore))
	query.Set("cursor_time", target.Timestamp.Format(time.RFC3339Nano))
	query.Set("cursor_id", strconv.FormatInt(target.ID, 10))
	query.Set("limit", "1")

	resp, err := http.Get(srv.URL + "/api/logs/context/page?" + query.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body logContextPageResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "svc-a", body.DeploymentID)
	assert.Equal(t, store.ContextPageBefore, body.Direction)
	assert.True(t, body.HasMore)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "api near", body.Items[0].Message)
}

type searchLogBackend struct {
	query         logbackend.SearchQuery
	entries       []model.LogEntry
	next          logbackend.Cursor
	hasMore       bool
	err           error
	contextQuery  logbackend.ContextQuery
	contextResult logbackend.ContextResult
	contextErr    error
}

func (b *searchLogBackend) Query(ctx context.Context, f logbackend.QueryFilter) ([]model.LogEntry, logbackend.Cursor, error) {
	return nil, logbackend.Cursor{}, nil
}

func (b *searchLogBackend) Search(ctx context.Context, q logbackend.SearchQuery) ([]model.LogEntry, logbackend.Cursor, bool, error) {
	b.query = q
	return b.entries, b.next, b.hasMore, b.err
}

func (b *searchLogBackend) Context(ctx context.Context, q logbackend.ContextQuery) (logbackend.ContextResult, error) {
	b.contextQuery = q
	return b.contextResult, b.contextErr
}

func (b *searchLogBackend) Subscribe(ctx context.Context, opts logbackend.SubscribeOptions) logbackend.LogStream {
	ch := make(chan model.LogEntry)
	close(ch)
	return logbackend.LogStream{Ch: ch, Cancel: func() {}}
}
