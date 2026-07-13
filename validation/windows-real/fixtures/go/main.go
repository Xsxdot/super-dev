// Command go-fixture 提供 Windows 真实验证的 Go 主服务。
//
// 职责：
//   - 提供 readiness、受保护正常请求、受控错误与浏览器交互页面
//   - 输出带 trace_id/request_id 的 JSON 结构化日志
//   - 周期经过一个稳定、无秘密变量的断点位置
//
// 边界：
//   - 不连接外部网络或持久化数据
//   - 不接受来自环境以外的长期凭据
//   - 不自行启动或管理 SuperDev 服务
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

const defaultPort = 18190

type server struct {
	logger     *slog.Logger
	campaignID string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	port := envInt("FIXTURE_PORT", defaultPort)
	campaignID := strings.TrimSpace(os.Getenv("FIXTURE_CAMPAIGN_ID"))
	if campaignID == "" {
		logger.Error("fixture configuration rejected", "stage", "startup", "cause", "FIXTURE_CAMPAIGN_ID is required")
		os.Exit(2)
	}

	app := &server{logger: logger.With("fixture", "go", "campaign_id", campaignID), campaignID: campaignID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", app.ready)
	mux.HandleFunc("GET /api/normal", app.normal)
	mux.HandleFunc("GET /api/error", app.controlledError)
	mux.HandleFunc("GET /api/ping", app.ping)
	mux.HandleFunc("GET /api/validation", app.validation)
	mux.HandleFunc("GET /", app.page)

	httpServer := &http.Server{
		Addr:              "127.0.0.1:" + strconv.Itoa(port),
		Handler:           requestContextMiddleware(app.logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	stopProbe := make(chan struct{})
	go runDebugProbeLoop(app.logger, stopProbe)
	go func() {
		app.logger.Info("fixture listening", "stage", "startup", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.logger.Error("fixture server failed", "stage", "serve", "address", httpServer.Addr, "cause", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	// Windows 没有 POSIX SIGTERM；只监听 Go 可移植的 Interrupt，受管强制结束由 SuperDev 负责。
	signal.Notify(sig, os.Interrupt)
	received := <-sig
	app.logger.Info("fixture shutdown requested", "stage", "shutdown", "signal", received.String())
	close(stopProbe)
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- httpServer.Close() }()
	if err := <-shutdownDone; err != nil {
		app.logger.Error("fixture shutdown failed", "stage", "shutdown", "cause", err)
		os.Exit(1)
	}
	app.logger.Info("fixture stopped", "stage", "shutdown", "outcome", "success")
}

func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("readiness checked", requestLogFields(r)...)
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "provider": "go"})
}

func (s *server) normal(w http.ResponseWriter, r *http.Request) {
	expected := authorizationForCampaign(s.campaignID)
	supplied := strings.TrimSpace(r.Header.Get("Authorization"))
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) != 1 {
		s.logger.Warn("authenticated request rejected", append(requestLogFields(r), "reason", "authorization mismatch")...)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "code": "unauthorized"})
		return
	}
	s.logger.Info("authenticated request completed", append(requestLogFields(r), "outcome", "success")...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": "go"})
}

// authorizationForCampaign 从非秘密 campaign ID 确定性推导本轮临时 Authorization。
// 返回值只在单次请求校验期间存在，不从环境或持久化配置读取完整凭据。
func authorizationForCampaign(campaignID string) string {
	return "Bearer superdev-validation-" + campaignID
}

func (s *server) controlledError(w http.ResponseWriter, r *http.Request) {
	s.logger.Error("controlled fixture error", append(requestLogFields(r), "error_code", "fixture_controlled_error", "cause", "requested validation failure")...)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "code": "fixture_controlled_error"})
}

func (s *server) ping(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("browser ping completed", append(requestLogFields(r), "outcome", "success")...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "pong"})
}

func (s *server) validation(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	if runID == "" {
		s.logger.Warn("browser validation rejected", append(requestLogFields(r), "reason", "run_id missing")...)
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "code": "run_id_required"})
		return
	}
	s.logger.Info("browser validation completed", append(requestLogFields(r), "run_id", runID, "outcome", "success")...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run_id": runID})
}

func (s *server) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>SuperDev Windows Validation</title></head>
<body data-testid="validation-page">
  <p data-testid="validation-ready">ready</p>
  <label>Message <input data-testid="validation-input" value="initial"></label>
  <label>Mode <select data-testid="validation-select"><option value="mac">macOS</option><option value="windows">Windows</option></select></label>
  <button data-testid="validation-submit" type="button">Validate</button>
  <output data-testid="validation-result">idle</output>
  <script>
    const params = new URLSearchParams(window.location.search); const runID = params.get('run_id') || 'missing-run-id';
    const input = document.querySelector('[data-testid="validation-input"]');
    const result = document.querySelector('[data-testid="validation-result"]');
    async function validate() {
      const url = '/api/validation?trace_id=browser-validation&request_id=' + encodeURIComponent(input.value) + '&run_id=' + encodeURIComponent(runID);
      const response = await fetch(url); const payload = await response.json(); result.textContent = payload.run_id || payload.code;
      console.info('superdev-windows-validation', runID, payload.ok);
    }
    document.querySelector('[data-testid="validation-submit"]').addEventListener('click', validate);
    input.addEventListener('keydown', event => { if (event.key === 'Enter') validate(); });
  </script>
</body></html>`))
	if err != nil {
		s.logger.Error("browser page write failed", append(requestLogFields(r), "cause", err)...)
	}
}

func runDebugProbeLoop(logger *slog.Logger, stop <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	probeCount := 0
	for {
		select {
		case <-stop:
			logger.Debug("debug probe loop stopped", "stage", "shutdown", "count", probeCount)
			return
		case <-ticker.C:
			probeCount++
			debugProbe(logger, probeCount)
			if probeCount%10 == 0 {
				// 周期受控错误保证纯 MCP 日志场景无需旁路 HTTP 刺激也能获得稳定错误证据。
				logger.Error("controlled background fixture error", "stage", "debug_probe", "trace_id", "go-error-trace", "request_id", "go-error-request", "error_code", "fixture_controlled_error", "cause", "scheduled validation signal")
			}
		}
	}
}

func debugProbe(logger *slog.Logger, probeCount int) {
	validationMarker := "superdev-breakpoint-ready"
	requestID := "debug-probe-request"
	counter := probeCount
	probeVisible := debugProbeHelper(validationMarker, requestID, counter) // SUPERDEV_BREAKPOINT: stable step-in call with non-secret locals.
	logger.Debug("debug probe", "stage", "debug_probe", "message", validationMarker, "request_id", requestID, "count", counter, "visible", probeVisible)
}

//go:noinline
func debugProbeHelper(validationMarker, requestID string, counter int) bool {
	return validationMarker != "" && requestID != "" && counter > 0
}

func requestContextMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("request entered", requestLogFields(r)...)
		next.ServeHTTP(w, r)
		logger.Debug("request exited", append(requestLogFields(r), "outcome", "handled")...)
	})
}

func requestLogFields(r *http.Request) []any {
	return []any{
		"method", r.Method,
		"path", r.URL.Path,
		"trace_id", r.URL.Query().Get("trace_id"),
		"request_id", r.URL.Query().Get("request_id"),
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
