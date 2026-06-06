// handler_run_logs_ws_test.go 验证 pipeline run 实时日志 WebSocket。
//
// 职责：
//   - 验证 /ws/runs/{runId}/logs 会先回放持久化日志
//   - 验证订阅期间的 RunHub 事件会继续推送
//   - 验证 done 事件会发给客户端并关闭连接
//
// 边界：
//   - 不测试 pipeline engine 执行
//   - 不测试前端去重逻辑
package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestWsRunLogsReplaysStreamsAndClosesOnDone(t *testing.T) {
	app := newTestAppForPackage(t)
	run := model.Run{
		ID:         "run-1",
		ProjectID:  "p1",
		PipelineID: "deploy",
		Status:     model.RunStatusRunning,
		StartedAt:  100,
	}
	require.NoError(t, app.store.SaveRun(run))
	replay, err := app.store.AppendRunLogLine("run-1", "Build", "", "stdout", "replay", 101)
	require.NoError(t, err)

	conn := dialAppWebSocket(t, app, "/ws/runs/run-1/logs")

	var first RunEvent
	require.NoError(t, conn.ReadJSON(&first))
	require.NotNil(t, first.Log)
	assert.Equal(t, RunEventKindLog, first.Kind)
	assert.Equal(t, replay.ID, first.Log.ID)
	assert.Equal(t, "replay", first.Log.Line)

	live := model.RunLogLine{ID: replay.ID + 1, RunID: "run-1", StepName: "Deploy", Stream: "stdout", Line: "live", At: 102}
	app.runHub.Broadcast("run-1", RunEvent{Kind: RunEventKindLog, Log: &live})

	var second RunEvent
	require.NoError(t, conn.ReadJSON(&second))
	require.NotNil(t, second.Log)
	assert.Equal(t, "live", second.Log.Line)

	final := run
	final.Status = model.StatusSuccess
	final.FinishedAt = 200
	app.runHub.Broadcast("run-1", RunEvent{Kind: RunEventKindDone, Run: &final})

	var done RunEvent
	require.NoError(t, conn.ReadJSON(&done))
	require.NotNil(t, done.Run)
	assert.Equal(t, RunEventKindDone, done.Kind)
	assert.Equal(t, model.StatusSuccess, done.Run.Status)

	require.Eventually(t, func() bool {
		var ev RunEvent
		return conn.ReadJSON(&ev) != nil
	}, time.Second, 10*time.Millisecond)
}

func TestWsRunLogsRejectsMissingRun(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/runs/missing/logs"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if conn != nil {
		_ = conn.Close()
	}

	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
