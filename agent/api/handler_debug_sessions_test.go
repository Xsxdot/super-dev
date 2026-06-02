// Package api 验证 debug session HTTP API。
//
// 职责：
//   - 验证 session 创建、事件追加、读取、关闭
//   - 验证 project/deployment 边界校验
//
// 边界：
//   - 不调用 MCP 工具
//   - 不启动真实服务进程
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/debugsession"
	"github.com/superdev/agent/model"
)

func TestDebugSessionAPI_CreateAppendGetClose(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(debugSessionAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	createBody := map[string]any{
		"project_id":    "proj-debug",
		"deployment_id": "api-dev",
		"title":         "API failure",
		"question":      "Why does api-dev fail?",
	}
	session := postJSONForTest[debugsession.Session](t, srv.URL+"/api/debug-sessions", createBody, http.StatusOK)
	require.NotEmpty(t, session.ID)
	assert.Equal(t, debugsession.StatusOpen, session.Status)

	event := postJSONForTest[debugsession.Event](t, srv.URL+"/api/debug-sessions/"+session.ID+"/events", map[string]any{
		"type":    debugsession.EventObservation,
		"actor":   debugsession.ActorAssistant,
		"summary": "api-dev emitted retry exhausted",
		"data": map[string]any{
			"evidence_ids": []int{42},
		},
	}, http.StatusOK)
	assert.Equal(t, debugsession.EventObservation, event.Type)

	detailResp, err := http.Get(srv.URL + "/api/debug-sessions/" + session.ID)
	require.NoError(t, err)
	defer detailResp.Body.Close()
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	var detail struct {
		Session debugsession.Session `json:"session"`
		Events  []debugsession.Event `json:"events"`
	}
	require.NoError(t, json.NewDecoder(detailResp.Body).Decode(&detail))
	assert.Equal(t, session.ID, detail.Session.ID)
	assert.Len(t, detail.Events, 2)

	closed := postJSONForTest[debugsession.Session](t, srv.URL+"/api/debug-sessions/"+session.ID+"/close", map[string]any{
		"summary": "collected enough evidence",
	}, http.StatusOK)
	assert.Equal(t, debugsession.StatusClosed, closed.Status)
}

func TestDebugSessionAPI_RejectsDeploymentOutsideProject(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(debugSessionAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	postJSONForTest[map[string]string](t, srv.URL+"/api/debug-sessions", map[string]any{
		"project_id":    "proj-debug",
		"deployment_id": "worker-dev",
		"title":         "bad deployment",
		"question":      "bad deployment",
	}, http.StatusBadRequest)
}

func TestDebugSessionAPI_RejectsAppendToClosedSession(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(debugSessionAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	session := postJSONForTest[debugsession.Session](t, srv.URL+"/api/debug-sessions", map[string]any{
		"project_id": "proj-debug",
		"title":      "closed",
		"question":   "closed",
	}, http.StatusOK)
	_ = postJSONForTest[debugsession.Session](t, srv.URL+"/api/debug-sessions/"+session.ID+"/close", map[string]any{}, http.StatusOK)

	postJSONForTest[map[string]string](t, srv.URL+"/api/debug-sessions/"+session.ID+"/events", map[string]any{
		"type":    debugsession.EventNote,
		"actor":   debugsession.ActorAssistant,
		"summary": "late",
	}, http.StatusBadRequest)
}

func debugSessionAPIProject() model.Project {
	return model.Project{
		ID:   "proj-debug",
		Name: "debug-demo",
		Services: []model.Service{{
			ID:        "svc-api",
			ProjectID: "proj-debug",
			Name:      "api",
			Deployments: []model.Deployment{{
				ID:      "api-dev",
				EnvName: "dev",
			}},
		}},
	}
}

func postJSONForTest[T any](t *testing.T, url string, body any, status int) T {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, status, resp.StatusCode)
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}
