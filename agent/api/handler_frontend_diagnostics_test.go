package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xsxdot/super-dev/agent/logbuf"
)

// TestFrontendDiagnostics 验证打点事件写入 logbuf 且带 __desktop__ 归属。
func TestFrontendDiagnostics(t *testing.T) {
	buf := logbuf.New(nil, 100, "test-node")
	defer buf.Close()
	a := &App{buf: buf}

	body := `{"events":[{"scope":"log-panel","level":"warn","event":"scroll_intent.transition","at":"2026-07-03T04:00:00Z","from":"follow-bottom","to":"idle"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/frontend-diagnostics", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.frontendDiagnostics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	recent := buf.Recent(10)
	if len(recent) != 1 {
		t.Fatalf("want 1 entry, got %d", len(recent))
	}
	e := recent[0]
	if e.DeploymentID != FrontendDiagnosticsDeploymentID {
		t.Fatalf("deployment=%s", e.DeploymentID)
	}
	if e.Level != "WARN" || !strings.Contains(e.Message, "scroll_intent.transition") {
		t.Fatalf("entry=%+v", e)
	}
}

// TestFrontendDiagnosticsLimit 验证超过 500 条报 400。
func TestFrontendDiagnosticsLimit(t *testing.T) {
	buf := logbuf.New(nil, 100, "test-node")
	defer buf.Close()
	a := &App{buf: buf}
	var sb strings.Builder
	sb.WriteString(`{"events":[`)
	for i := 0; i < 501; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"scope":"log-panel","level":"debug","event":"e","at":"2026-07-03T04:00:00Z"}`)
	}
	sb.WriteString(`]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/frontend-diagnostics", strings.NewReader(sb.String()))
	rec := httptest.NewRecorder()
	a.frontendDiagnostics(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
