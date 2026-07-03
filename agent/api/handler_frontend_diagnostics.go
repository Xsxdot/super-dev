// handler_frontend_diagnostics.go 实现桌面端前端诊断打点接收接口。
//
// 职责：
//   - POST /api/frontend-diagnostics：接收 desktop 批量上报的面板诊断事件，
//     转换为 LogEntry 写入 logbuf（自动获得折叠、落库、tail_logs/search_logs 可查能力）
//
// 边界：
//   - 只接收结构化事件，不解析前端任意文本日志
//   - 不同步等待落库（logbuf 异步 flush），失败由 logbuf 统一记录
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// FrontendDiagnosticsDeploymentID 是桌面端打点日志的内部虚拟 deployment 归属。
// 用双下划线前缀避免与真实 deployment ID（uuid/自定义名）冲突。
const FrontendDiagnosticsDeploymentID = "__desktop__"

// maxFrontendDiagnosticEvents 单次上报条数上限，防止异常前端把 agent 打爆。
const maxFrontendDiagnosticEvents = 500

type frontendDiagnosticsRequest struct {
	Events []map[string]any `json:"events"`
}

// frontendDiagnostics 处理 POST /api/frontend-diagnostics。
//
// body：{"events":[{"scope":...,"level":...,"event":...,"at":...,...任意上下文}]}
// 返回：{"accepted": n}；events 缺失或超上限时返回 400。
func (a *App) frontendDiagnostics(w http.ResponseWriter, r *http.Request) {
	var req frontendDiagnosticsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if len(req.Events) == 0 {
		jsonError(w, http.StatusBadRequest, "events is required")
		return
	}
	if len(req.Events) > maxFrontendDiagnosticEvents {
		log.Printf("[api] 前端打点单次上报超限 count=%d limit=%d，已拒绝", len(req.Events), maxFrontendDiagnosticEvents)
		jsonError(w, http.StatusBadRequest, "too many events")
		return
	}

	for _, ev := range req.Events {
		a.buf.Append(frontendDiagnosticEntry(ev))
	}
	jsonOK(w, map[string]any{"accepted": len(req.Events)})
}

// frontendDiagnosticEntry 把一条前端事件转换为 LogEntry。
//
// level 映射到日志级别（默认 DEBUG）；at 解析失败时用服务端当前时间兜底，
// 保证时间戳永不为零值（零值会破坏时间游标翻页）。
func frontendDiagnosticEntry(ev map[string]any) model.LogEntry {
	level := "DEBUG"
	if l, ok := ev["level"].(string); ok {
		switch strings.ToLower(l) {
		case "info":
			level = "INFO"
		case "warn":
			level = "WARN"
		case "error":
			level = "ERROR"
		}
	}
	ts := time.Now().UTC()
	if at, ok := ev["at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, at); err == nil {
			ts = parsed
		}
	}
	msg, err := json.Marshal(ev)
	if err != nil {
		// 理论上 map[string]any 从 JSON 反序列化而来必可再序列化；兜底不丢事件。
		msg = []byte(`{"event":"marshal-failed"}`)
	}
	return model.LogEntry{
		DeploymentID: FrontendDiagnosticsDeploymentID,
		Timestamp:    ts,
		Level:        level,
		Message:      string(msg),
		Stream:       "frontend",
	}
}
