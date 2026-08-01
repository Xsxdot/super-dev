package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestDeploymentLogsEndpoint_NotFound(t *testing.T) {
	app := newTestAppInstance(t)
	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/nonexistent/logs")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeploymentSearchEndpoint_NotFound(t *testing.T) {
	app := newTestAppInstance(t)
	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/nonexistent/search?q=error")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeploymentSearchEndpoint_RequiresQ(t *testing.T) {
	app := newTestAppInstance(t)
	depID := addTestDeploymentBackend(t, app)

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/" + depID + "/search")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestDeploymentLogsEndpoint_ReturnsEmptyArray(t *testing.T) {
	app := newTestAppInstance(t)
	depID := addTestDeploymentBackend(t, app)

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/" + depID + "/logs")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Items []model.LogEntry `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.NotNil(t, result.Items)
}

func TestDeploymentLogsEndpoint_ScopesQueryToPathDeploymentID(t *testing.T) {
	app := newTestAppInstance(t)
	depID := "dep-scoped"
	backend := &recordingLogBackend{
		queryEntries: []model.LogEntry{{
			ID:           1,
			DeploymentID: depID,
			RunID:        "run-1",
			Timestamp:    time.Now(),
			Message:      "vite ready",
			Stream:       "stderr",
		}},
	}
	app.SetBackendForTest(depID, backend)

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

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
	assert.Equal(t, logbackend.Cursor{ID: "88"}, backend.queryFilter.Before)
}

func TestDeploymentLogsEndpoint_BeforeTime(t *testing.T) {
	app := newTestAppInstance(t)
	depID := "dep-before-time"
	backend := &recordingLogBackend{}
	app.SetBackendForTest(depID, backend)

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	cut := time.Date(2026, 7, 3, 4, 0, 1, 123, time.UTC)
	resp, err := http.Get(srv.URL + "/api/deployments/" + depID + "/logs?before_time=" + cut.Format(time.RFC3339Nano))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, backend.queryFilter.BeforeTime.Equal(cut), "before_time should be parsed and passed to backend")

	resp, err = http.Get(srv.URL + "/api/deployments/" + depID + "/logs?before_time=not-a-time")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestDeploymentLogsWebSocket_ScopesSubscriptionToPathDeploymentID(t *testing.T) {
	app := newTestAppInstance(t)
	depID := "dep-ws-scoped"
	backend := &recordingLogBackend{subscribeOptions: make(chan logbackend.SubscribeOptions, 1)}
	app.SetBackendForTest(depID, backend)

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/deployments/" + depID + "/logs"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	select {
	case got := <-backend.subscribeOptions:
		assert.Equal(t, depID, got.DeploymentID)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for backend subscription")
	}
}

func TestDeploymentSearchEndpoint_ReturnsResults(t *testing.T) {
	app := newTestAppInstance(t)
	depID := addTestDeploymentBackend(t, app)

	app.WriteTestLog(model.LogEntry{
		DeploymentID: "svc-test",
		RunID:        "r1",
		Timestamp:    time.Now(),
		Message:      "error happened",
		Stream:       "stderr",
	})
	time.Sleep(200 * time.Millisecond)

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/" + depID + "/search?q=error&limit=10")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Query string           `json:"query"`
		Items []model.LogEntry `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "error", result.Query)
}

type recordingLogBackend struct {
	queryFilter      logbackend.QueryFilter
	queryEntries     []model.LogEntry
	subscribeOptions chan logbackend.SubscribeOptions
}

func (b *recordingLogBackend) Query(ctx context.Context, f logbackend.QueryFilter) ([]model.LogEntry, logbackend.Cursor, error) {
	b.queryFilter = f
	return b.queryEntries, logbackend.Cursor{}, nil
}

func (b *recordingLogBackend) Search(ctx context.Context, q logbackend.SearchQuery) ([]model.LogEntry, logbackend.Cursor, bool, error) {
	return nil, logbackend.Cursor{}, false, nil
}

func (b *recordingLogBackend) Subscribe(ctx context.Context, opts logbackend.SubscribeOptions) logbackend.LogStream {
	ch := make(chan model.LogEntry)
	if b.subscribeOptions != nil {
		b.subscribeOptions <- opts
	}
	close(ch)
	return logbackend.LogStream{
		Ch:     ch,
		Cancel: func() {},
	}
}
