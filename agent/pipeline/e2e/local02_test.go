//go:build e2e

// Package e2e_test runs real local-02 pipeline deployment verification.
//
// Responsibilities:
//   - Execute example pipelines through the real pipeline engine.
//   - Prepare local-02 runtime dependencies and systemd deployment root.
//   - Verify each deployed service through HTTP health checks.
//
// Boundaries:
//   - Only runs with the e2e build tag.
//   - Does not run as part of normal go test ./....
package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
	"github.com/superdev/agent/pipeline/plugins"
	pipelinetemplate "github.com/superdev/agent/template"
	"gopkg.in/yaml.v3"
)

type exampleCase struct {
	Name string `json:"name"`
	Port string `json:"port"`
}

type exampleResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// TestLocal02Examples builds and deploys every example pipeline to local-02.
//
// 注意：
//   - 该测试会通过 SSH 安装/使用目标机运行时依赖。
//   - 需要设置 SUPERDEV_E2E_LOCAL02_KEY 或 SUPERDEV_E2E_LOCAL02_PASSWORD。
func TestLocal02Examples(t *testing.T) {
	cfg := local02Config(t)
	root := repoRoot(t)
	results := make([]exampleResult, 0)
	cases := []exampleCase{
		{Name: "go-http", Port: "18080"},
		{Name: "node-http", Port: "18081"},
		{Name: "python-http", Port: "18082"},
		{Name: "java-springboot", Port: "18083"},
		{Name: "rust-http", Port: "18084"},
		{Name: "php-http", Port: "18085"},
		{Name: "vue-go-combined", Port: "18086"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	sshExec := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		if hostID != "local-02" {
			return model.Host{}, false
		}
		return cfg, true
	})
	require.NoError(t, prepareLocal02(ctx, sshExec))

	localRunRoot := t.TempDir()
	for _, tc := range cases {
		err := runExamplePipeline(ctx, t, root, filepath.Join(localRunRoot, tc.Name), cfg, sshExec, tc)
		result := exampleResult{Name: tc.Name, Status: "success"}
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	writeReport(t, root, results)
	for _, result := range results {
		require.Equal(t, "success", result.Status, result.Name+": "+result.Error)
	}
}

func local02Config(t *testing.T) model.Host {
	t.Helper()
	host := getenv("SUPERDEV_E2E_LOCAL02_HOST", "100.90.99.61")
	user := getenv("SUPERDEV_E2E_LOCAL02_USER", "root")
	port := getenvInt(t, "SUPERDEV_E2E_LOCAL02_PORT", 22)
	keyPath := os.Getenv("SUPERDEV_E2E_LOCAL02_KEY")
	password := os.Getenv("SUPERDEV_E2E_LOCAL02_PASSWORD")
	if keyPath == "" {
		keyPath = firstExistingSSHKey()
	}
	require.True(t, keyPath != "" || password != "", "set SUPERDEV_E2E_LOCAL02_KEY or SUPERDEV_E2E_LOCAL02_PASSWORD")
	return model.Host{ID: "local-02", Name: host, SSHHost: host, SSHPort: port, SSHUser: user, SSHPassword: password, SSHKeyPath: keyPath}
}

func firstExistingSSHKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		candidate := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func prepareLocal02(ctx context.Context, ex *pipeline.SSHExecutor) error {
	cmd := `mkdir -p /opt/superdev-examples
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y curl tar gzip nodejs npm python3 openjdk-17-jre-headless php maven cargo
fi`
	return ex.RunRemote(ctx, pipeline.Target{HostID: "local-02", HostName: "local-02"}, cmd, "", func(string, string) {})
}

func runExamplePipeline(ctx context.Context, t *testing.T, root string, runTempDir string, host model.Host, ex *pipeline.SSHExecutor, tc exampleCase) error {
	t.Helper()
	builtins, err := pipelinetemplate.LoadBuiltins()
	if err != nil {
		return err
	}
	resolver := pipelinetemplate.NewStore(filepath.Join(runTempDir, "templates"), builtins, root)
	data, err := os.ReadFile(filepath.Join(root, "examples", tc.Name, "pipeline.yaml"))
	if err != nil {
		return err
	}
	var p model.Pipeline
	if err := yaml.Unmarshal(data, &p); err != nil {
		return err
	}
	p.Variables = pipeline.MergeVariables(p.Variables, pipeline.RuntimeReservedVars(runTempDir, pipeline.ReservedVarOptions{
		Workspace: root,
		Version:   time.Now().Format("20060102150405"),
		Env:       "local02",
	}))
	p.Variables = pipelinetemplate.RenderPipelineVariableMap(p.Variables)
	p.Build, err = pipelinetemplate.ExpandSteps(p.Build, resolver, p.Variables, 5)
	if err != nil {
		return err
	}
	p.Deploy, err = pipelinetemplate.ExpandSteps(p.Deploy, resolver, p.Variables, 5)
	if err != nil {
		return err
	}
	plan, run, err := pipeline.BuildPlan(tc.Name, p, []model.HostRef{{ID: "local-02", Name: host.SSHHost}})
	if err != nil {
		return err
	}
	engine := pipeline.NewEngine()
	engine.Register(plugins.NewLocalCommand())
	engine.Register(plugins.NewArchivePackage())
	engine.Register(plugins.NewRemoteCommand(ex))
	engine.Register(plugins.NewTransfer(ex))
	engine.Register(plugins.NewHTTPCheck(nil))
	_, err = engine.Run(ctx, plan, run, func(event pipeline.Event) {
		logPipelineEvent(t, tc, event)
	})
	return err
}

func logPipelineEvent(t *testing.T, tc exampleCase, event pipeline.Event) {
	t.Helper()
	switch event.Type {
	case pipeline.EventTaskLog:
		line := strings.TrimSpace(event.Line)
		if line != "" {
			t.Logf("%s %s %s: %s", tc.Name, event.StepName, event.Stream, line)
		}
	case pipeline.EventTaskFinished:
		t.Logf("%s %s finished: %s", tc.Name, event.StepName, event.Status)
	}
}

func writeReport(t *testing.T, root string, results []exampleResult) {
	t.Helper()
	dir := filepath.Join(root, "artifacts", "e2e")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "local-02-report.json"), data, 0o644))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	require.NoError(t, err)
	return parsed
}
