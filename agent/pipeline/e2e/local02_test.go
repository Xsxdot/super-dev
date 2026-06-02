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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
	"github.com/superdev/agent/pipeline/plugins"
	"github.com/superdev/agent/store"
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
	require.NoError(t, prepareLocalBuildToolchains(ctx, t))
	sshExec := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		if hostID != "local-02" {
			return model.Host{}, false
		}
		return cfg, true
	})
	require.NoError(t, prepareLocal02(ctx, t, sshExec))

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

func TestLocal02ProjectPipelineHTTPDeploy(t *testing.T) {
	cfg := local02Config(t)
	root := repoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	require.NoError(t, prepareLocalBuildToolchains(ctx, t))
	sshExec := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		if hostID != "local-02" {
			return model.Host{}, false
		}
		return cfg, true
	})
	require.NoError(t, prepareLocal02(ctx, t, sshExec))

	app, err := api.NewApp(api.AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	requestAppJSON(t, app, http.MethodPost, "/api/hosts", cfg, http.StatusOK)

	projectDir := writeHTTPExampleProjectConfig(t, root)
	requestAppJSON(t, app, http.MethodPost, "/api/projects", map[string]string{"root_path": projectDir}, http.StatusOK)

	version := "http-" + time.Now().Format("20060102150405")
	deployRR := requestAppJSON(t, app, http.MethodPost,
		"/api/projects/local02-http-project/pipelines/deploy-go-http/deploy",
		map[string]any{"env_name": "local02", "variables": map[string]string{"version": version}},
		http.StatusOK,
	)
	var run model.Run
	require.NoError(t, json.NewDecoder(deployRR.Body).Decode(&run))
	require.Equal(t, model.StatusSuccess, run.Status)
	require.Equal(t, version, run.ArtifactVersion)

	runsRR := requestAppJSON(t, app, http.MethodGet,
		"/api/projects/local02-http-project/pipelines/deploy-go-http/runs", nil, http.StatusOK)
	var runsResp struct {
		Items []model.Run `json:"items"`
	}
	require.NoError(t, json.NewDecoder(runsRR.Body).Decode(&runsResp))
	require.NotEmpty(t, runsResp.Items)
	require.Equal(t, run.ID, runsResp.Items[0].ID)

	logsRR := requestAppJSON(t, app, http.MethodGet,
		"/api/projects/local02-http-project/pipelines/deploy-go-http/runs/"+run.ID+"/logs?step_name=E2E%20Deploy%20Log&host_id=local-02&limit=20",
		nil,
		http.StatusOK,
	)
	var logsResp struct {
		Items []model.RunLogLine `json:"items"`
	}
	require.NoError(t, json.NewDecoder(logsRR.Body).Decode(&logsResp))
	require.NotEmpty(t, logsResp.Items)
	require.Contains(t, logsResp.Items[0].Line, "http-e2e-deploy-log")
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

func prepareLocalBuildToolchains(ctx context.Context, t *testing.T) error {
	t.Helper()
	cmd := exec.CommandContext(ctx, "sh", "-c", `if command -v rustup >/dev/null 2>&1; then
  if ! rustup target list --installed | grep -q '^x86_64-unknown-linux-musl$'; then
    RUSTUP_DIST_SERVER=https://static.rust-lang.org RUSTUP_UPDATE_ROOT=https://static.rust-lang.org/rustup rustup toolchain install stable --profile minimal --target x86_64-unknown-linux-musl --no-self-update
  fi
fi`)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				t.Logf("prepare local build toolchains: %s", line)
			}
		}
	}
	return err
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

func prepareLocal02(ctx context.Context, t *testing.T, ex *pipeline.SSHExecutor) error {
	t.Helper()
	cmd := `mkdir -p /opt/superdev-examples
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y curl tar gzip nodejs python3 openjdk-17-jre-headless php
fi`
	return ex.RunRemote(ctx, pipeline.Target{HostID: "local-02", HostName: "local-02"}, cmd, "", func(line, stream string) {
		line = strings.TrimSpace(line)
		if line != "" {
			t.Logf("prepare local-02 %s: %s", stream, line)
		}
	})
}

func writeHTTPExampleProjectConfig(t *testing.T, root string) string {
	t.Helper()
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	config := fmt.Sprintf(`
id: local02-http-project
name: local02-http-project
variables:
  repo_root: %q
environments:
  - name: local02
services:
  - id: svc-go-http
    name: go-http
    deployments:
      - id: dep-go-http-local02
        env: local02
        location: remote
        hosts: [local-02]
pipelines:
  - id: deploy-go-http
    name: Deploy Go HTTP
    services: [go-http]
    artifact_kind: file
    variables:
      example_dir: ${repo_root}/examples/go-http
      artifact: ${artifacts}/go-http-http-e2e-${version}.tar.gz
    roles:
      local02_targets:
        from_service: go-http
    pipeline:
      build:
        - name: Build Go
          type: include
          with:
            template: builtin://go-binary-build
            version: 1.0.0
            vars:
              work_dir: ${example_dir}
              package: .
              output: ${output}/go-http
              build_env: GOOS=linux GOARCH=amd64 CGO_ENABLED=0
              artifact: ${artifact}
              files:
                - from: ${output}/go-http
                  to: go-http
      deploy:
        - name: Deploy Go
          type: include
          with:
            template: builtin://systemd-seamless-deploy
            version: 1.0.0
            vars:
              role: local02_targets
              artifact: ${artifact}
              release_dir: /opt/superdev-examples/go-http-http-e2e/releases
              current_dir: /opt/superdev-examples/go-http-http-e2e/current
              app_name: go-http-http-e2e
              service_name: superdev-example-go-http-http-e2e
              exec_start: /opt/superdev-examples/go-http-http-e2e/current/go-http
              working_dir: /opt/superdev-examples/go-http-http-e2e/current
              port: "18180"
              health_path: /health
              environment: Environment=PORT=18180 APP_VERSION=${version}
        - name: E2E Deploy Log
          type: remote_command
          roles: ["local02_targets"]
          needs: [Deploy Go]
          with:
            cmd: printf http-e2e-deploy-log
`, root)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(config), 0o644))
	return projectDir
}

func requestAppJSON(t *testing.T, app *api.App, method string, path string, body any, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, wantStatus, rr.Code, rr.Body.String())
	return rr
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
	version := time.Now().Format("20060102150405")
	p.Variables = pipeline.MergeVariables(p.Variables, pipeline.RuntimeReservedVars(runTempDir, pipeline.ReservedVarOptions{
		Workspace: root,
		Version:   version,
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
	run.ID = tc.Name + "-" + version
	run.ProjectID = "local02-examples"
	run.PipelineID = tc.Name
	run.EnvName = "local02"
	run.ArtifactVersion = version
	runStore, err := store.New(":memory:")
	if err != nil {
		return err
	}
	defer runStore.Close()
	if err := runStore.SaveRun(run); err != nil {
		return err
	}
	deploySteps := stepNameSet(p.Deploy)
	var persistMu sync.Mutex
	var persistErr error
	recordPersistErr := func(err error) {
		if err == nil {
			return
		}
		persistMu.Lock()
		defer persistMu.Unlock()
		if persistErr == nil {
			persistErr = err
		}
	}
	engine := pipeline.NewEngine()
	engine.Register(plugins.NewLocalCommand())
	engine.Register(plugins.NewArchivePackage())
	engine.Register(plugins.NewRemoteCommand(ex))
	engine.Register(plugins.NewTransfer(ex))
	engine.Register(plugins.NewHTTPCheck(nil))
	final, err := engine.RunWithOptions(ctx, plan, run, func(event pipeline.Event) {
		logPipelineEvent(t, tc, event)
		if event.Type == pipeline.EventTaskLog {
			recordPersistErr(runStore.AppendRunLog(run.ID, event.StepName, event.HostID, event.Stream, event.Line, event.At))
		}
		if event.Type == pipeline.EventTaskFinished && event.HostID != "" && deploySteps[event.StepName] {
			line := fmt.Sprintf("task finished: %s", event.Status)
			recordPersistErr(runStore.AppendRunLog(run.ID, event.StepName, event.HostID, "status", line, event.At))
		}
	}, pipeline.RunOptions{
		OnRunUpdate: func(next model.Run) {
			recordPersistErr(runStore.SaveRun(next))
		},
	})
	recordPersistErr(runStore.SaveRun(final))
	persistMu.Lock()
	firstPersistErr := persistErr
	persistMu.Unlock()
	if err != nil {
		return err
	}
	if firstPersistErr != nil {
		return firstPersistErr
	}
	return assertPersistedExampleRun(runStore, final, deploySteps)
}

func stepNameSet(steps []model.Step) map[string]bool {
	out := map[string]bool{}
	for _, step := range steps {
		out[step.Name] = true
	}
	return out
}

func assertPersistedExampleRun(runStore *store.Store, run model.Run, deploySteps map[string]bool) error {
	if run.ArtifactVersion == "" {
		return fmt.Errorf("run %s artifact version is empty", run.ID)
	}
	hasHostTask := false
	for _, stepRun := range run.StepRuns {
		for _, task := range stepRun.Tasks {
			if task.HostID != "" {
				hasHostTask = true
				break
			}
		}
	}
	if !hasHostTask {
		return fmt.Errorf("run %s has no host task", run.ID)
	}
	lines, err := runStore.ReadRunLogs(store.RunLogQuery{RunID: run.ID, Limit: 1000})
	if err != nil {
		return err
	}
	for _, line := range lines {
		if line.HostID != "" && deploySteps[line.StepName] {
			return nil
		}
	}
	return fmt.Errorf("run %s has no stored deploy log with host", run.ID)
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
