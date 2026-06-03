// handler_exec.go 实现远端 agent 命令执行 WebSocket 接口。
//
// 职责：
//   - 接收单条命令执行请求
//   - 调用 remoteexec.Executor 在 agent 本机执行
//   - 把 stdout/stderr/exit/error 消息写回 WebSocket
//
// 边界：
//   - 不解析 pipeline step
//   - 不做 SSH fallback
//   - 不实现 manifest 授权，授权由注入的 Authorizer 决定
package api

import (
	"net/http"
	"sync"

	"github.com/superdev/agent/remoteexec"
)

// wsExec 处理 GET /ws/exec，在 agent 本机执行一条命令并流式回传结果。
//
// 注意：
//   - 每个连接只接收一条 CommandRequest
//   - 命令执行前 remoteexec.Executor 会调用 Authorizer.Authorize
func (a *App) wsExec(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var req remoteexec.CommandRequest
	if err := conn.ReadJSON(&req); err != nil {
		_ = conn.WriteJSON(remoteexec.Message{Type: remoteexec.MessageError, Error: err.Error()})
		return
	}

	executor := remoteexec.NewExecutor(a.executionAuthorizer)
	var writeMu sync.Mutex
	emit := func(msg remoteexec.Message) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(msg)
	}
	if err := executor.Execute(r.Context(), req, emit); err != nil {
		_ = emit(remoteexec.Message{Type: remoteexec.MessageError, Error: err.Error()})
	}
}

// execHealth 处理 GET /api/exec/health，用于 agent 版本兼容探测。
func (a *App) execHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
