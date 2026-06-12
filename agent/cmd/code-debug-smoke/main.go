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
	"strconv"
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
	if os.Getenv("SUPERDEV_CODE_DEBUG_ATTACH") == "1" {
		if err := runAttachSmoke(client, base, deploymentID); err != nil {
			printJSON("attach", map[string]any{"ok": false, "message": err.Error()})
			os.Exit(1)
		}
		return
	}
	runLaunchSmoke(client, base, deploymentID, keepRuntime)
}

func runLaunchSmoke(client *http.Client, base string, deploymentID string, keepRuntime bool) {
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

func runAttachSmoke(client *http.Client, base string, deploymentID string) error {
	source := strings.TrimSpace(os.Getenv("SUPERDEV_CODE_DEBUG_SOURCE"))
	line := intEnv("SUPERDEV_CODE_DEBUG_LINE", 0)
	if source == "" || line <= 0 {
		return fmt.Errorf("SUPERDEV_CODE_DEBUG_SOURCE and SUPERDEV_CODE_DEBUG_LINE are required for attach smoke")
	}
	threadID := intEnv("SUPERDEV_CODE_DEBUG_THREAD_ID", 1)
	timeoutMS := intEnv("SUPERDEV_CODE_DEBUG_TIMEOUT_MS", 5000)
	target, err := findDebugTarget(client, base, deploymentID)
	if err != nil {
		return err
	}
	projectID, _ := target["project_id"].(string)
	if projectID == "" {
		return fmt.Errorf("debug target %s did not include project_id", deploymentID)
	}
	printJSON("target", map[string]any{"ok": true, "deployment_id": deploymentID, "target": target})

	if _, err := postJSON[map[string]any](client, base+"/api/deployments/"+url.PathEscape(deploymentID)+"/start", map[string]any{"mode": "normal"}); err != nil {
		return fmt.Errorf("start normal deployment: %w", err)
	}
	pidBefore, err := waitDeploymentPID(client, base, deploymentID, 5*time.Second)
	if err != nil {
		return err
	}
	printJSON("normal_started", map[string]any{"ok": true, "deployment_id": deploymentID, "pid": pidBefore})

	capture, err := postJSON[map[string]any](client, base+"/api/deployments/"+url.PathEscape(deploymentID)+"/debug/capture", map[string]any{
		"source":     source,
		"line":       line,
		"thread_id":  threadID,
		"timeout_ms": timeoutMS,
	})
	if err != nil {
		return fmt.Errorf("capture via attach: %w", err)
	}
	printJSON("capture", map[string]any{"ok": true, "deployment_id": deploymentID, "capture": capture})

	debugger, err := deploymentDebugger(client, base, projectID, deploymentID)
	if err != nil {
		return err
	}
	if origin, _ := debugger["origin"].(string); origin != "attached" {
		return fmt.Errorf("debugger origin = %q, want attached", origin)
	}
	pidAfter, err := deploymentPID(client, base, deploymentID)
	if err != nil {
		return err
	}
	if pidAfter != pidBefore {
		return fmt.Errorf("deployment pid changed during attach: before=%d after=%d", pidBefore, pidAfter)
	}
	printJSON("attached_runtime", map[string]any{"ok": true, "deployment_id": deploymentID, "pid": pidAfter, "debugger": debugger})

	sessionID, _ := capture["session_id"].(string)
	if sessionID == "" {
		return fmt.Errorf("capture result did not include session_id")
	}
	if err := closeDebugSessionStopRuntime(client, base, sessionID); err != nil {
		return fmt.Errorf("detach close session: %w", err)
	}
	pidDetached, err := waitDeploymentPID(client, base, deploymentID, 5*time.Second)
	if err != nil {
		return err
	}
	if pidDetached != pidBefore {
		return fmt.Errorf("deployment pid changed after detach: before=%d after=%d", pidBefore, pidDetached)
	}
	printJSON("detached_runtime", map[string]any{"ok": true, "deployment_id": deploymentID, "pid": pidDetached})
	return nil
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

func findDebugTarget(client *http.Client, base string, deploymentID string) (map[string]any, error) {
	targets, err := getJSON[[]map[string]any](client, strings.TrimRight(base, "/")+"/api/code-debug-targets")
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if id, _ := target["deployment_id"].(string); id == deploymentID {
			return target, nil
		}
	}
	return nil, fmt.Errorf("debug target %s not found", deploymentID)
}

func waitDeploymentPID(client *http.Client, base string, deploymentID string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		pid, err := deploymentPID(client, base, deploymentID)
		if err == nil && pid > 0 {
			return pid, nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("deployment %s did not report a running pid", deploymentID)
}

func deploymentPID(client *http.Client, base string, deploymentID string) (int, error) {
	services, err := getJSON[[]map[string]any](client, strings.TrimRight(base, "/")+"/api/services")
	if err != nil {
		return 0, err
	}
	for _, service := range services {
		deployments, _ := service["deployments"].([]any)
		for _, raw := range deployments {
			dep, _ := raw.(map[string]any)
			if id, _ := dep["id"].(string); id != deploymentID {
				continue
			}
			return intFromJSON(dep["pid"]), nil
		}
	}
	return 0, fmt.Errorf("deployment %s not found in /api/services", deploymentID)
}

func deploymentDebugger(client *http.Client, base string, projectID string, deploymentID string) (map[string]any, error) {
	status, err := getJSON[map[string]any](client, strings.TrimRight(base, "/")+"/api/projects/"+url.PathEscape(projectID)+"/runtime-status")
	if err != nil {
		return nil, err
	}
	envs, _ := status["environments"].([]any)
	for _, rawEnv := range envs {
		env, _ := rawEnv.(map[string]any)
		instances, _ := env["instances"].([]any)
		for _, rawInst := range instances {
			inst, _ := rawInst.(map[string]any)
			if id, _ := inst["deployment_id"].(string); id != deploymentID {
				continue
			}
			debugger, _ := inst["debugger"].(map[string]any)
			if debugger == nil {
				return nil, fmt.Errorf("deployment %s has no debugger in runtime status", deploymentID)
			}
			return debugger, nil
		}
	}
	return nil, fmt.Errorf("deployment %s not found in runtime status", deploymentID)
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

func closeDebugSessionStopRuntime(client *http.Client, base string, sessionID string) error {
	_, err := postJSON[map[string]any](
		client,
		strings.TrimRight(base, "/")+"/api/code-debug-sessions/"+url.PathEscape(sessionID)+"/close",
		map[string]any{"stop_runtime": true},
	)
	return err
}

func emitRuntimeAfterClose(client *http.Client, base string, deploymentID string) {
	payload := map[string]any{
		"deployment_id":           deploymentID,
		"expected_debugger_state": "attached",
		"expected_origin":         "launched",
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

func intEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func intFromJSON(value any) int {
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func printJSON(step string, data map[string]any) {
	data["step"] = step
	raw, _ := json.Marshal(data)
	fmt.Println(string(raw))
}
