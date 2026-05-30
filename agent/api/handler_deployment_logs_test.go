package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
