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
