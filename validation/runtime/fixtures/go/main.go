// runtime-validation Go fixture 提供跨平台、无外部依赖的 HTTP 运行与断点合同。
//
// 职责：
//   - 暴露 readiness、正常 probe 和受控错误 probe
//   - 周期产生可检索 ERROR，并在后台路径保留可由 dlv 观察的稳定局部变量
//
// 边界：
//   - 不访问 SuperDev API，不持久化数据，也不拥有 MCP coverage
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

const controlledErrorTraceID = "go-error-trace"

func main() {
	port := os.Getenv("FIXTURE_PORT")
	if port == "" {
		panic("FIXTURE_PORT is required")
	}
	campaignID := os.Getenv("FIXTURE_CAMPAIGN_ID")
	go emitControlledErrors(campaignID)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ready": true, "provider": "go"})
	})
	http.HandleFunc("/api/probe", probe)
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(browserFixturePage))
	})
	if err := http.ListenAndServe("127.0.0.1:"+port, nil); err != nil {
		panic(err)
	}
}

const browserFixturePage = `<!doctype html>
<html><body>
  <main data-testid="validation-ready" data-state="ready">
    <input data-testid="validation-input" />
    <select data-testid="validation-select"><option value="runtime">runtime</option></select>
    <button data-testid="validation-submit">run</button>
    <output data-testid="validation-result"></output>
  </main>
	  <script>
	    document.querySelector('[data-testid=validation-submit]').addEventListener('click', async () => {
	      const value = document.querySelector('[data-testid=validation-input]').value;
	      console.info('runtime-validation-submit', value);
	      const response = await fetch('/api/probe?run_id=' + encodeURIComponent(value), { method: 'POST' });
	      document.querySelector('[data-testid=validation-result]').textContent = value + ':' + response.status;
	    });
  </script>
</body></html>`

func probe(w http.ResponseWriter, r *http.Request) {
	controlledError := r.URL.Query().Get("mode") == "error"
	fixtureMarker, fixtureCount, fixtureProvider := observeFixtureState()
	status := http.StatusOK
	if controlledError {
		status = http.StatusInternalServerError
		writeFixtureLog("ERROR", "runtime validation controlled error trace_id="+controlledErrorTraceID, map[string]any{
			"trace_id": controlledErrorTraceID, "campaign_id": os.Getenv("FIXTURE_CAMPAIGN_ID"), "source": "http",
		})
	}
	writeJSON(w, status, map[string]any{
		"ok": status == http.StatusOK, "provider": fixtureProvider, "count": fixtureCount, "marker": fixtureMarker,
	})
}

func observeFixtureState() (string, int, string) {
	fixtureMarker := "breakpoint-visible"
	fixtureCount := 42
	fixtureProvider := "go"
	// 使用运行期等价的零增量，保证断点行有真实机器指令且三个局部变量同时存活。
	fixtureCount += len(fixtureMarker) - len("breakpoint-visible") // SUPERDEV_FIXTURE_BREAKPOINT
	return fixtureMarker, fixtureCount, fixtureProvider
}

func emitControlledErrors(campaignID string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		// 后台也经过稳定断点行，保证顺序 MCP 调用无需另启并发触发器。
		_, _, _ = observeFixtureState()
		writeFixtureLog("ERROR", "runtime validation controlled error trace_id="+controlledErrorTraceID, map[string]any{
			"trace_id": controlledErrorTraceID, "campaign_id": campaignID, "source": "ticker",
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeFixtureLog(level, message string, fields map[string]any) {
	// 夹具必须保持零三方依赖，故写入与 Agent log parser 兼容的结构化 JSON，而不引入产品日志包。
	entry := map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "msg": message}
	for key, value := range fields {
		entry[key] = value
	}
	_ = json.NewEncoder(os.Stdout).Encode(entry)
}
