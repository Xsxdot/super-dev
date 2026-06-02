// Package main 提供 MCP Log Lab 的端到端 smoke 验证命令。
//
// 职责：
//   - 启动临时 SuperDev agent
//   - 注册 examples/mcp-log-lab 项目的临时副本
//   - 通过 superdev-mcp stdio JSON-RPC 调用 MCP 工具
//   - 断言服务控制、日志 tail、日志搜索、上下文、诊断和 debug session 结果
//
// 边界：
//   - 不读写用户真实 ~/.superdev 数据
//   - 不直接读取 SuperDev SQLite 日志库
//   - 不替代单元测试，只做真实链路 smoke
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	projectName   = "mcp-log-lab"
	targetTraceID = "mcp-lab-target"
)

type smokeConfig struct {
	agentBin  string
	mcpBin    string
	workspace string
	keepData  bool
}

type agentProcess struct {
	cmd     *exec.Cmd
	baseURL string
	logs    *bytes.Buffer
	done    chan error
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type callToolResult struct {
	Content           []map[string]string `json:"content"`
	StructuredContent map[string]any      `json:"structuredContent,omitempty"`
	IsError           bool                `json:"isError,omitempty"`
}

type mcpClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	logs   *bytes.Buffer
	done   chan error
	nextID int
}

func main() {
	cfg := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL", err)
		os.Exit(1)
	}
}

func parseFlags() smokeConfig {
	var cfg smokeConfig
	flag.StringVar(&cfg.agentBin, "agent-bin", "", "path to superdev-agent binary")
	flag.StringVar(&cfg.mcpBin, "mcp-bin", "", "path to superdev-mcp binary")
	flag.StringVar(&cfg.workspace, "workspace", "", "path to super-debug workspace")
	flag.BoolVar(&cfg.keepData, "keep-data", false, "keep temporary data directory")
	flag.Parse()
	return cfg
}

func run(ctx context.Context, cfg smokeConfig) error {
	if cfg.agentBin == "" {
		return errors.New("--agent-bin is required")
	}
	if cfg.mcpBin == "" {
		return errors.New("--mcp-bin is required")
	}
	workspace, err := resolveWorkspace(cfg.workspace)
	if err != nil {
		return err
	}
	agentBin, err := filepath.Abs(cfg.agentBin)
	if err != nil {
		return err
	}
	mcpBin, err := filepath.Abs(cfg.mcpBin)
	if err != nil {
		return err
	}
	dataDir, err := os.MkdirTemp("", "superdev-mcp-log-lab-*")
	if err != nil {
		return fmt.Errorf("create temp data dir: %w", err)
	}
	if cfg.keepData {
		fmt.Println("INFO keeping data dir", dataDir)
	} else {
		defer os.RemoveAll(dataDir)
	}

	fixtureCopy := filepath.Join(dataDir, "project", "mcp-log-lab")
	if err := copyDir(filepath.Join(workspace, "examples", "mcp-log-lab"), fixtureCopy); err != nil {
		return fmt.Errorf("copy fixture: %w", err)
	}

	agent, err := startAgent(ctx, agentBin, dataDir)
	if err != nil {
		return err
	}
	defer agent.close()
	if _, err := registerProject(ctx, agent.baseURL, fixtureCopy); err != nil {
		return err
	}

	mcp, err := startMCP(ctx, mcpBin, agent.baseURL)
	if err != nil {
		return err
	}
	defer mcp.close()
	defer cleanupDeployments(context.Background(), mcp)

	if err := assertProjectVisible(ctx, mcp); err != nil {
		return err
	}
	pass("list_projects")
	if err := assertServicesVisible(ctx, mcp); err != nil {
		return err
	}
	pass("list_services")

	for _, depID := range []string{"api-dev", "worker-dev", "noisy-dev"} {
		if _, err := mcp.callTool(ctx, "start_service", map[string]any{"deployment_id": depID}); err != nil {
			return fmt.Errorf("start %s: %w", depID, err)
		}
		pass("start " + strings.TrimSuffix(depID, "-dev"))
	}

	if _, err := waitForPayload(ctx, 15*time.Second, func() (map[string]any, bool, error) {
		payload, err := mcp.callTool(ctx, "tail_logs", map[string]any{
			"deployment_id":       "api-dev",
			"limit":               80,
			"apply_project_rules": false,
		})
		if err != nil {
			return nil, false, err
		}
		return payload, strings.Contains(fmt.Sprint(payload), "trace_id="+targetTraceID), nil
	}); err != nil {
		return fmt.Errorf("tail api logs: %w", err)
	}
	pass("tail api logs")

	noisyPayload, err := mcp.callTool(ctx, "tail_logs", map[string]any{
		"deployment_id": "noisy-dev",
		"limit":         80,
	})
	if err != nil {
		return fmt.Errorf("tail noisy logs: %w", err)
	}
	if strings.Contains(fmt.Sprint(noisyPayload), "HEARTBEAT") {
		return errors.New("tail noisy logs still contains HEARTBEAT after project rules")
	}
	pass("tail noisy logs with rules")

	searchPayload, err := waitForPayload(ctx, 15*time.Second, func() (map[string]any, bool, error) {
		payload, err := mcp.callTool(ctx, "search_logs", map[string]any{
			"project_name": projectName,
			"q":            "trace_id=" + targetTraceID,
			"limit":        80,
		})
		if err != nil {
			return nil, false, err
		}
		return payload, firstLogID(payload) > 0, nil
	})
	if err != nil {
		return fmt.Errorf("search trace: %w", err)
	}
	logID := firstLogID(searchPayload)
	pass("search trace")

	contextPayload, err := mcp.callTool(ctx, "get_log_context", map[string]any{
		"project_name": projectName,
		"id":           logID,
		"before_ms":    5000,
		"after_ms":     5000,
		"limit":        80,
	})
	if err != nil {
		return fmt.Errorf("get log context: %w", err)
	}
	if !hasContextRows(contextPayload) {
		return errors.New("get_log_context returned no context rows")
	}
	pass("get log context")

	if _, err := mcp.callTool(ctx, "start_service", map[string]any{"deployment_id": "crasher-dev"}); err != nil {
		return fmt.Errorf("start crasher: %w", err)
	}
	if err := waitForDeploymentStatus(ctx, mcp, "crasher-dev", "failed"); err != nil {
		return err
	}

	diagnosis, err := mcp.callTool(ctx, "diagnose_service", map[string]any{"deployment_id": "crasher-dev"})
	if err != nil {
		return fmt.Errorf("diagnose crasher: %w", err)
	}
	diagnosisText := fmt.Sprint(diagnosis)
	if !strings.Contains(diagnosisText, "failed") && !strings.Contains(diagnosisText, "retry exhausted") {
		return errors.New("diagnose_service did not include failed status or recent error evidence")
	}
	pass("diagnose crasher")

	sessionPayload, err := mcp.callTool(ctx, "create_debug_session", map[string]any{
		"project_name": projectName,
		"title":        "MCP log lab smoke",
		"question":     "Can MCP persist trace and error evidence?",
	})
	if err != nil {
		return fmt.Errorf("create debug session: %w", err)
	}
	sessionID := stringField(sessionPayload, "data", "session", "id")
	if sessionID == "" {
		return errors.New("create_debug_session returned no session id")
	}
	pass("create debug session")

	tracePayload, err := mcp.callTool(ctx, "analyze_trace_logs", map[string]any{
		"project_name": projectName,
		"trace_id":     targetTraceID,
		"limit":        80,
	})
	if err != nil {
		return fmt.Errorf("analyze trace logs: %w", err)
	}
	if !strings.Contains(fmt.Sprint(tracePayload), targetTraceID) && !strings.Contains(fmt.Sprint(tracePayload), "timeline") {
		return errors.New("analyze_trace_logs did not include trace timeline evidence")
	}
	pass("analyze trace logs")

	errorPayload, err := mcp.callTool(ctx, "summarize_error_window", map[string]any{
		"project_name":  projectName,
		"deployment_id": "crasher-dev",
		"since":         "2m",
		"limit":         80,
	})
	if err != nil {
		return fmt.Errorf("summarize error window: %w", err)
	}
	if !strings.Contains(fmt.Sprint(errorPayload), "retry_exhausted") && !strings.Contains(fmt.Sprint(errorPayload), "connection_refused") {
		return errors.New("summarize_error_window did not include expected failure signals")
	}
	pass("summarize error window")

	appendPayload, err := mcp.callTool(ctx, "append_log_analysis_to_session", map[string]any{
		"session_id":    sessionID,
		"analysis_type": "trace",
		"project_name":  projectName,
		"trace_id":      targetTraceID,
		"limit":         80,
	})
	if err != nil {
		return fmt.Errorf("append log analysis: %w", err)
	}
	if !strings.Contains(fmt.Sprint(appendPayload), "event") {
		return errors.New("append_log_analysis_to_session returned no event")
	}
	pass("append log analysis")

	detailPayload, err := mcp.callTool(ctx, "get_debug_session", map[string]any{
		"session_id": sessionID,
		"limit":      20,
	})
	if err != nil {
		return fmt.Errorf("get debug session: %w", err)
	}
	if !strings.Contains(fmt.Sprint(detailPayload), "log_analysis") {
		return errors.New("get_debug_session did not include log_analysis event")
	}
	pass("get debug session")

	cleanupDeployments(ctx, mcp)
	pass("cleanup")
	return nil
}

func startAgent(ctx context.Context, agentBin, dataDir string) (*agentProcess, error) {
	addr, err := freeAddr()
	if err != nil {
		return nil, err
	}
	logs := &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, agentBin, "-addr", addr, "-data", dataDir)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent: %w", err)
	}
	proc := &agentProcess{
		cmd:     cmd,
		baseURL: "http://" + addr,
		logs:    logs,
		done:    make(chan error, 1),
	}
	go func() { proc.done <- cmd.Wait() }()
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := waitForAgent(waitCtx, proc.baseURL); err != nil {
		proc.close()
		return nil, fmt.Errorf("%w; agent logs: %s", err, logs.String())
	}
	return proc, nil
}

func (p *agentProcess) close() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if p.cmd.ProcessState == nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
}

func waitForAgent(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/projects", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for agent: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func registerProject(ctx context.Context, baseURL, projectRoot string) (string, error) {
	body, err := json.Marshal(map[string]string{"root_path": projectRoot})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/projects", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("register project: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("register project status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &project); err != nil {
		return "", err
	}
	if project.Name != projectName {
		return "", fmt.Errorf("registered project name %q, want %q", project.Name, projectName)
	}
	return project.ID, nil
}

func startMCP(ctx context.Context, mcpBin, agentURL string) (*mcpClient, error) {
	logs := &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, mcpBin)
	cmd.Env = append(os.Environ(), "SUPERDEV_AGENT_URL="+agentURL)
	cmd.Stderr = logs
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp: %w", err)
	}
	client := &mcpClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		logs:   logs,
		done:   make(chan error, 1),
		nextID: 1,
	}
	go func() { client.done <- cmd.Wait() }()
	if _, err := client.callRPC(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "mcp-log-lab-smoke",
			"version": "1.0.0",
		},
	}); err != nil {
		client.close()
		return nil, err
	}
	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		client.close()
		return nil, err
	}
	return client, nil
}

func (c *mcpClient) callTool(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	raw, err := c.callRPC(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	var result callToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse %s result: %w", name, err)
	}
	if result.IsError {
		return nil, fmt.Errorf("%s tool error: %s", name, contentText(result.Content))
	}
	if result.StructuredContent == nil {
		return map[string]any{}, nil
	}
	return result.StructuredContent, nil
}

func (c *mcpClient) callRPC(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeJSON(req); err != nil {
		return nil, err
	}
	line, err := c.stdout.ReadString('\n')
	if err != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, fmt.Errorf("read %s response: %w; mcp logs: %s", method, err, c.logs.String())
		}
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, fmt.Errorf("parse %s response %q: %w", method, strings.TrimSpace(line), err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s rpc error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func (c *mcpClient) notify(method string, params map[string]any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeJSON(req)
}

func (c *mcpClient) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c *mcpClient) close() {
	if c == nil {
		return
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil && c.cmd.ProcessState == nil {
		select {
		case <-c.done:
			return
		case <-time.After(500 * time.Millisecond):
			_ = c.cmd.Process.Kill()
		}
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}

func assertProjectVisible(ctx context.Context, mcp *mcpClient) error {
	payload, err := mcp.callTool(ctx, "list_projects", map[string]any{})
	if err != nil {
		return err
	}
	if !strings.Contains(fmt.Sprint(payload), projectName) {
		return fmt.Errorf("list_projects did not contain %s", projectName)
	}
	return nil
}

func assertServicesVisible(ctx context.Context, mcp *mcpClient) error {
	payload, err := mcp.callTool(ctx, "list_services", map[string]any{"project_name": projectName})
	if err != nil {
		return err
	}
	text := fmt.Sprint(payload)
	for _, service := range []string{"api", "worker", "noisy", "crasher"} {
		if !strings.Contains(text, service) {
			return fmt.Errorf("list_services missing %s: %s", service, text)
		}
	}
	return nil
}

func waitForDeploymentStatus(ctx context.Context, mcp *mcpClient, deploymentID, want string) error {
	_, err := waitForPayload(ctx, 15*time.Second, func() (map[string]any, bool, error) {
		payload, err := mcp.callTool(ctx, "list_services", map[string]any{"project_name": projectName})
		if err != nil {
			return nil, false, err
		}
		return payload, deploymentStatus(payload, deploymentID) == want, nil
	})
	if err != nil {
		return fmt.Errorf("wait for %s status %s: %w", deploymentID, want, err)
	}
	return nil
}

func cleanupDeployments(ctx context.Context, mcp *mcpClient) {
	if mcp == nil {
		return
	}
	for _, depID := range []string{"api-dev", "worker-dev", "noisy-dev", "crasher-dev"} {
		_, _ = mcp.callTool(ctx, "stop_service", map[string]any{"deployment_id": depID})
	}
}

func waitForPayload(ctx context.Context, timeout time.Duration, fn func() (map[string]any, bool, error)) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		payload, ok, err := fn()
		if err != nil {
			lastErr = err
		} else if ok {
			return payload, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, errors.New("condition not reached before timeout")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func firstLogID(payload map[string]any) int64 {
	for _, entry := range entries(payload) {
		if id, ok := numberField(entry, "id"); ok && id > 0 {
			return int64(id)
		}
	}
	return 0
}

func entries(payload map[string]any) []map[string]any {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return nil
	}
	rawEntries, ok := data["entries"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rawEntries))
	for _, raw := range rawEntries {
		entry, ok := raw.(map[string]any)
		if ok {
			out = append(out, entry)
		}
	}
	return out
}

func hasContextRows(payload map[string]any) bool {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return false
	}
	contextPayload, ok := data["context"].(map[string]any)
	if !ok {
		return false
	}
	items, ok := contextPayload["items_by_deployment"].(map[string]any)
	if !ok {
		return false
	}
	for _, raw := range items {
		rows, ok := raw.([]any)
		if ok && len(rows) > 0 {
			return true
		}
	}
	return false
}

func deploymentStatus(payload map[string]any, deploymentID string) string {
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return ""
	}
	rawServices, ok := data["services"].([]any)
	if !ok {
		return ""
	}
	for _, rawService := range rawServices {
		service, ok := rawService.(map[string]any)
		if !ok {
			continue
		}
		rawDeployments, ok := service["deployments"].([]any)
		if !ok {
			continue
		}
		for _, rawDeployment := range rawDeployments {
			deployment, ok := rawDeployment.(map[string]any)
			if !ok {
				continue
			}
			if deployment["id"] == deploymentID {
				if status, ok := deployment["status"].(string); ok {
					return status
				}
			}
		}
	}
	return ""
}

func numberField(entry map[string]any, name string) (float64, bool) {
	value, ok := entry[name]
	if !ok {
		return 0, false
	}
	number, ok := value.(float64)
	return number, ok
}

func stringField(payload map[string]any, path ...string) string {
	var cur any = payload
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[key]
	}
	s, _ := cur.(string)
	return s
}

func contentText(content []map[string]string) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		parts = append(parts, item["text"])
	}
	return strings.Join(parts, " ")
}

func resolveWorkspace(raw string) (string, error) {
	if raw != "" {
		return filepath.Abs(raw)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "agent", "go.mod")) &&
			fileExists(filepath.Join(dir, "examples", "mcp-log-lab", ".superdev", "config.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("workspace not found; pass --workspace")
		}
		dir = parent
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func freeAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func pass(label string) {
	fmt.Println("PASS " + label)
}
