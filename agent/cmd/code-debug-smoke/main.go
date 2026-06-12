// Command code-debug-smoke runs a real local code debug smoke test through the agent API.
//
// 职责：
//   - 验证 agent code-debug HTTP API 的真实集成路径
//   - 在 adapter 缺失时输出可读跳过原因
//
// 边界：
//   - 不安装调试器
//   - 不修改项目配置
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	base := strings.TrimRight(os.Getenv("SUPERDEV_AGENT_URL"), "/")
	deploymentID := strings.TrimSpace(os.Getenv("SUPERDEV_DEPLOYMENT_ID"))
	if base == "" {
		base = "http://127.0.0.1:57017"
	}
	if deploymentID == "" {
		printJSON("config", map[string]any{"ok": false, "message": "SUPERDEV_DEPLOYMENT_ID is required"})
		os.Exit(1)
	}
	keepRuntime := os.Getenv("SUPERDEV_CODE_DEBUG_KEEP_RUNTIME") == "1"
	client := &http.Client{Timeout: 15 * time.Second}
	printJSON("target", map[string]any{"ok": true, "deployment_id": deploymentID})
	session, err := postJSON[map[string]any](client, base+"/api/code-debug-sessions", map[string]any{"deployment_id": deploymentID})
	if err != nil {
		printJSON("open", map[string]any{"ok": false, "message": err.Error()})
		os.Exit(1)
	}
	printJSON("open", map[string]any{"ok": true, "session": session})
	if id, _ := session["session_id"].(string); id != "" {
		if err := closeDebugSession(client, base, id, keepRuntime); err != nil {
			printJSON("close", map[string]any{"ok": false, "session_id": id, "message": err.Error()})
			os.Exit(1)
		}
		printJSON("close", map[string]any{"ok": true, "session_id": id})
		if keepRuntime {
			emitRuntimeAfterClose(client, base, deploymentID)
		}
	}
}

func postJSON[T any](client *http.Client, url string, body any) (T, error) {
	var zero T
	raw, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var body struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body.Error == "" {
			body.Error = resp.Status
		}
		if body.Code != "" {
			return zero, fmt.Errorf("%s: %s", body.Code, body.Error)
		}
		return zero, fmt.Errorf("http status %d: %s", resp.StatusCode, body.Error)
	}
	var out T
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func getJSON[T any](client *http.Client, endpoint string) (T, error) {
	var zero T
	resp, err := client.Get(endpoint)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return zero, fmt.Errorf("http status %d", resp.StatusCode)
	}
	var out T
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func closeDebugSession(client *http.Client, base string, sessionID string, keepRuntime bool) error {
	closeReq := map[string]any{}
	if keepRuntime {
		closeReq["stop_runtime"] = false
	}
	_, err := postJSON[map[string]any](
		client,
		strings.TrimRight(base, "/")+"/api/code-debug-sessions/"+url.PathEscape(sessionID)+"/close",
		closeReq,
	)
	return err
}

func emitRuntimeAfterClose(client *http.Client, base string, deploymentID string) {
	payload := map[string]any{
		"deployment_id": deploymentID,
		"expected":      "debug-running",
	}
	targets, targetsErr := getJSON[[]map[string]any](client, strings.TrimRight(base, "/")+"/api/code-debug-targets")
	if targetsErr != nil {
		payload["targets_error"] = targetsErr.Error()
	} else {
		payload["targets"] = targets
	}
	sessions, sessionsErr := getJSON[[]map[string]any](client, strings.TrimRight(base, "/")+"/api/code-debug-sessions")
	if sessionsErr != nil {
		payload["sessions_error"] = sessionsErr.Error()
	} else {
		payload["sessions"] = sessions
	}
	printJSON("runtime_after_close", payload)
}

func printJSON(step string, data map[string]any) {
	data["step"] = step
	raw, _ := json.Marshal(data)
	fmt.Println(string(raw))
}
