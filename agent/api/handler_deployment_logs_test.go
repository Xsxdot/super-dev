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
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

func TestDeploymentLogsEndpoint_NotFound(t *testing.T) {
	app := newTestAppInstance(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/nonexistent/logs")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeploymentSearchEndpoint_NotFound(t *testing.T) {
	app := newTestAppInstance(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/nonexistent/search?q=error")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeploymentSearchEndpoint_RequiresQ(t *testing.T) {
	app := newTestAppInstance(t)
	depID := addTestDeploymentBackend(t, app)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/deployments/" + depID + "/search")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestDeploymentLogsEndpoint_ReturnsEmptyArray(t *testing.T) {
	app := newTestAppInstance(t)
	depID := addTestDeploymentBackend(t, app)

	srv := httptest.NewServer(app.Handler())
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

	srv := httptest.NewServer(app.Handler())
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
	assert.Equal(t, int64(88), backend.queryFilter.BeforeID)
}

func TestDeploymentLogsWebSocket_ScopesSubscriptionToPathDeploymentID(t *testing.T) {
	app := newTestAppInstance(t)
	depID := "dep-ws-scoped"
	backend := &recordingLogBackend{subscribeIDs: make(chan string, 1)}
	app.SetBackendForTest(depID, backend)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/deployments/" + depID + "/logs"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	select {
	case got := <-backend.subscribeIDs:
		assert.Equal(t, depID, got)
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

	srv := httptest.NewServer(app.Handler())
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
	queryFilter  logbackend.QueryFilter
	queryEntries []model.LogEntry
	subscribeIDs chan string
}

func (b *recordingLogBackend) Query(ctx context.Context, f logbackend.QueryFilter) ([]model.LogEntry, logbackend.Cursor, error) {
	b.queryFilter = f
	return b.queryEntries, logbackend.Cursor{}, nil
}

func (b *recordingLogBackend) Search(ctx context.Context, q logbackend.SearchQuery) ([]model.LogEntry, logbackend.Cursor, bool, error) {
	return nil, logbackend.Cursor{}, false, nil
}

func (b *recordingLogBackend) Subscribe(ctx context.Context, deploymentID string) logbackend.LogStream {
	ch := make(chan model.LogEntry)
	if b.subscribeIDs != nil {
		b.subscribeIDs <- deploymentID
	}
	close(ch)
	return logbackend.LogStream{
		Ch:     ch,
		Cancel: func() {},
	}
}
