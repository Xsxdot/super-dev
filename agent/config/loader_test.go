package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestLoadProject(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yaml := `
name: myapp
environments:
  - name: dev
    is_dev: true
    order: 0
services:
  - name: server
    required: true
    order: 1
    deployments:
      - env: dev
        location: local
        command: go run .
        working_dir: ./server
  - name: worker
    order: 2
    deployments:
      - env: dev
        location: local
        command: go run ./worker
env_selected_service_ids:
  dev:
    - worker
log_rules:
  - id: "rule-1"
    name: exclude health
    type: exclude
    keywords: ["healthcheck"]
    logic: or
    enabled: true
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yaml), 0o644))

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)

	assert.Equal(t, "myapp", p.Name)
	assert.Equal(t, dir, p.RootPath)
	assert.Len(t, p.Services, 2)
	assert.Equal(t, "server", p.Services[0].Name)
	assert.True(t, p.Services[0].Required)
	assert.Equal(t, 1, p.Services[0].Order)
	// 相对路径应被解析为相对于项目根目录的绝对路径
	assert.Equal(t, filepath.Join(dir, "server"), p.Services[0].Deployments[0].WorkDir)
	assert.Equal(t, []string{"worker"}, p.EnvSelectedServiceIDs["dev"])
	assert.Equal(t, model.StatusStopped, p.Services[0].Status)
}

func TestLoaderBackfillsServiceLanguage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".superdev", "config.yaml"), []byte(`
name: demo
services:
  - name: api
    deployments:
      - env: dev
        location: local
        control_mode: managed
        command: go run ./cmd/api
`), 0o644))

	p, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	require.Len(t, p.Services, 1)
	assert.Equal(t, model.LanguageGo, p.Services[0].Language)
}

func TestLoaderBackfillsLanguageRuntimeFromRuntimeCWDMarker(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "web"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "web", "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".superdev", "config.yaml"), []byte(`
name: demo
services:
  - name: web
    deployments:
      - env: dev
        location: local
        control_mode: managed
        runtime:
          type: language
          cwd: ./web
          config:
            script: dev
`), 0o644))

	p, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	require.Len(t, p.Services, 1)
	assert.Equal(t, model.LanguageNode, p.Services[0].Language)
}

func TestLoaderBackfillsLanguageRuntimeFromConfigMarker(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".superdev", "config.yaml"), []byte(`
name: demo
services:
  - name: api
    deployments:
      - env: dev
        location: local
        control_mode: managed
        runtime:
          type: language
          cwd: .
          config:
            module: myapp.server
`), 0o644))

	p, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	require.Len(t, p.Services, 1)
	assert.Equal(t, model.LanguagePython, p.Services[0].Language)
}

func TestLoadProjectMissingFile(t *testing.T) {
	loader := config.NewLoader(t.TempDir())
	_, err := loader.Load()
	assert.ErrorIs(t, err, config.ErrNotFound)
}

func TestLoadLogRules(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yaml := `
name: myapp
services: []
log_rules:
  - id: "abc-123"
    name: exclude health
    type: exclude
    keywords: ["ping", "health"]
    logic: or
    enabled: true
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yaml), 0o644))

	loader := config.NewLoader(dir)
	rules, err := loader.LoadLogRules()
	require.NoError(t, err)
	assert.Len(t, rules, 1)
	assert.Equal(t, model.RuleTypeExclude, rules[0].Type)
	assert.Equal(t, []string{"ping", "health"}, rules[0].Keywords)
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	p := model.Project{
		ID:       "proj-1",
		Name:     "test",
		RootPath: dir,
		Services: []model.Service{
			{
				Name:  "api",
				Order: 0,
				Deployments: []model.Deployment{
					{EnvName: "dev", Location: model.LocationLocal, Command: "go run .", WorkDir: "."},
				},
			},
		},
		EnvSelectedServiceIDs: map[string][]string{"dev": {"api"}},
	}
	require.NoError(t, loader.Save(p))

	loaded, err := loader.Load()
	require.NoError(t, err)
	assert.Equal(t, "test", loaded.Name)
	// 全新目录默认落 split 格式，env_selected_service_ids 属 UI 状态，
	// split 格式下不再持久化进 yaml（已迁移为 agent 本地 store）。
	assert.Empty(t, loaded.EnvSelectedServiceIDs["dev"])
}

func TestSaveAndReloadWithCodeDebugConfig(t *testing.T) {
	dir := t.TempDir()
	project := model.Project{
		Name:     "debug-demo",
		RootPath: dir,
		Environments: []model.Environment{{
			Name:  "dev",
			IsDev: true,
		}},
		Services: []model.Service{{
			Name:     "api",
			Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Command:     "go run ./cmd/api",
				WorkDir:     dir,
				CodeDebug: &model.CodeDebugConfig{
					Policy:         model.CodeDebugPolicyEnabled,
					Mode:           model.CodeDebugModeLaunch,
					AdapterCommand: "dlv",
					AdapterArgs:    []string{"dap"},
					StopOnEntry:    true,
				},
			}},
		}},
	}

	loader := config.NewLoader(dir)
	require.NoError(t, loader.Save(project))
	loaded, err := loader.Load()
	require.NoError(t, err)

	assert.Equal(t, model.LanguageGo, loaded.Services[0].Language)
	got := loaded.Services[0].Deployments[0].CodeDebug
	require.NotNil(t, got)
	assert.Equal(t, model.CodeDebugPolicyEnabled, got.Policy)
	assert.Equal(t, model.CodeDebugModeLaunch, got.Mode)
	assert.Equal(t, "dlv", got.AdapterCommand)
	assert.Equal(t, []string{"dap"}, got.AdapterArgs)
	assert.True(t, got.StopOnEntry)
}

func TestSaveLogRules(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	p := model.Project{Name: "test", RootPath: dir}
	require.NoError(t, loader.Save(p))

	rules := []model.LogRule{
		{ID: "r1", Name: "no ping", Type: model.RuleTypeExclude, Keywords: []string{"ping"}, Logic: model.RuleLogicOR, Enabled: true},
	}
	require.NoError(t, loader.SaveLogRules(rules))

	loaded, err := loader.LoadLogRules()
	require.NoError(t, err)
	assert.Equal(t, "no ping", loaded[0].Name)
}

func TestLoadNewFormatProject(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yamlContent := `
name: myapp
environments:
  - name: dev
    is_dev: true
    order: 0
  - name: prod
    order: 1
services:
  - name: api-server
    order: 0
    deployments:
      - env: dev
        location: local
        command: "go run ./cmd/server"
        working_dir: "./server"
      - env: prod
        location: remote
        hosts: [prod-01]
        log_type: journalctl
        log_target: api-server.service
        start_command: "sudo systemctl start api-server"
        stop_command: "sudo systemctl stop api-server"
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yamlContent), 0o644))

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)

	assert.Equal(t, "myapp", p.Name)
	assert.Len(t, p.Environments, 2)
	assert.Equal(t, "dev", p.Environments[0].Name)
	assert.True(t, p.Environments[0].IsDev)
	assert.Equal(t, "prod", p.Environments[1].Name)
	assert.False(t, p.Environments[1].IsDev)

	assert.Len(t, p.Services, 1)
	svc := p.Services[0]
	assert.Equal(t, "api-server", svc.Name)
	assert.Len(t, svc.Deployments, 2)

	dev := svc.Deployments[0]
	assert.Equal(t, "dev", dev.EnvName)
	assert.Equal(t, model.LocationLocal, dev.Location)
	assert.Equal(t, "go run ./cmd/server", dev.Command)
	assert.Equal(t, filepath.Join(dir, "server"), dev.WorkDir)
	assert.False(t, dev.IsReadOnly())

	prod := svc.Deployments[1]
	assert.Equal(t, "prod", prod.EnvName)
	assert.Equal(t, model.LocationRemote, prod.Location)
	assert.Equal(t, []string{"prod-01"}, prod.HostIDs)
	assert.Equal(t, model.LogSourceTypeJournalctl, prod.LogType)
	assert.Equal(t, "api-server.service", prod.LogTarget)
	assert.Equal(t, "sudo systemctl start api-server", prod.StartCommand)
	assert.False(t, prod.IsReadOnly())
}

func TestLoadProjectRuntimeAndTopLevelPipelines(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".superdev", "config.yaml"), []byte(`
name: tk
variables:
  app_name: tk
  base_work_dir: /Users/xushixin/workspace/tk
environments:
  - name: dev
    is_dev: true
services:
  - name: server
    deployments:
      - env: dev
        location: local
        runtime:
          type: command
          command: "go run ./cmd/server"
          working_dir: server
          env_file: .env.dev
          env_vars:
            LOG_LEVEL: debug
        logs:
          type: process
pipelines:
  - id: deploy-dev
    name: Deploy Dev
    services: [server]
    variables:
      target_server: server_targets
    environments:
      dev:
        variables:
          config_file: resources/dev.yaml
    roles:
      server_targets:
        from_service: server
    pipeline:
      build:
        - name: Build
          type: local_command
          with:
            cmd: echo build
`), 0o644))

	p, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	assert.Equal(t, "tk", p.Variables["app_name"])
	require.Len(t, p.Services, 1)
	dep := p.Services[0].Deployments[0]
	require.NotNil(t, dep.Runtime)
	assert.Equal(t, model.RuntimeTypeCommand, dep.Runtime.Type)
	assert.Equal(t, filepath.Join(dir, "server"), dep.WorkDir)
	assert.Equal(t, "go run ./cmd/server", dep.Command)
	require.NotNil(t, dep.Logs)
	assert.Equal(t, model.LogKindProcess, dep.Logs.Type)
	require.Len(t, p.Pipelines, 1)
	assert.Equal(t, "deploy-dev", p.Pipelines[0].ID)
	assert.Equal(t, "server", p.Pipelines[0].Roles["server_targets"].FromService)
}

func TestLoaderPreservesLanguageRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(`
name: demo
environments:
  - name: dev
    is_dev: true
services:
  - name: api
    language: go
    deployments:
      - env: dev
        location: local
        control_mode: managed
        runtime:
          type: language
          cwd: ./server
          env:
            ENABLE: "true"
          config:
            program: ./cmd/server
`), 0o644))

	loader := config.NewLoader(dir)
	project, err := loader.Load()
	require.NoError(t, err)
	rt := project.Services[0].Deployments[0].Runtime
	require.NotNil(t, rt)
	assert.Equal(t, model.RuntimeTypeLanguage, rt.Type)
	// 路径字段存相对、内存持绝对（与 relativizeRuntime 对称）。这条断言原先期望
	// 逐字回显 "./server"——那是 relativizeRuntime 出现之前「两侧都不转换」的旧
	// 契约；save 侧开始相对化之后，load 侧不解析就成了单向漂移。
	assert.Equal(t, filepath.Join(dir, "server"), rt.CWD)
	assert.Equal(t, map[string]string{"ENABLE": "true"}, rt.Env)
	// Config 里的 provider 私有字段不属于路径相对化的范围，两侧都不转换，逐字保留。
	assert.Equal(t, "./cmd/server", rt.Config["program"])
}

func TestLoadNewFormatReadOnlyDeployment(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yamlContent := `
name: myapp
environments:
  - name: prod
    order: 0
services:
  - name: api-server
    deployments:
      - env: prod
        location: remote
        hosts: [prod-01]
        log_type: docker
        log_target: api-server
        read_only: true
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yamlContent), 0o644))

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)

	prod := p.Services[0].Deployments[0]
	assert.True(t, prod.ReadOnly)
}

func TestSaveAndReloadPreservesIsDev(t *testing.T) {
	// 验证 IsDev 在 Save/Load 往返后不丢失（model.Environment 无 yaml tag，
	// 必须经过 envsToYAML 转换才能正确序列化 is_dev key）
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	p := model.Project{
		Name:     "myapp",
		RootPath: dir,
		Environments: []model.Environment{
			{Name: "dev", IsDev: true, Order: 0},
			{Name: "prod", IsDev: false, Order: 1},
		},
		Services: []model.Service{},
	}
	require.NoError(t, loader.Save(p))

	loaded, err := loader.Load()
	require.NoError(t, err)
	require.Len(t, loaded.Environments, 2)
	assert.True(t, loaded.Environments[0].IsDev, "IsDev should survive Save/Load roundtrip")
	assert.False(t, loaded.Environments[1].IsDev)
}

func TestSaveAndReloadWithEnvironmentsAndDeployments(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	p := model.Project{
		ID:       "proj-1",
		Name:     "myapp",
		RootPath: dir,
		Environments: []model.Environment{
			{ID: "env-dev", Name: "dev", IsDev: true, Order: 0},
			{ID: "env-prod", Name: "prod", IsDev: false, Order: 1},
		},
		Services: []model.Service{
			{
				ID:    "svc-1",
				Name:  "api-server",
				Order: 0,
				Deployments: []model.Deployment{
					{
						ID:       "d-1",
						EnvName:  "dev",
						Location: model.LocationLocal,
						Command:  "go run .",
						WorkDir:  dir,
					},
					{
						ID:           "d-2",
						EnvName:      "prod",
						Location:     model.LocationRemote,
						HostIDs:      []string{"h-1"},
						LogType:      model.LogSourceTypeJournalctl,
						LogTarget:    "api-server.service",
						StartCommand: "systemctl start api-server",
						StopCommand:  "systemctl stop api-server",
						ReadOnly:     true,
					},
				},
			},
		},
		EnvSelectedServiceIDs: map[string][]string{"dev": {"api-server"}},
	}

	require.NoError(t, loader.Save(p))

	loaded, err := loader.Load()
	require.NoError(t, err)

	assert.Equal(t, "myapp", loaded.Name)
	assert.Len(t, loaded.Environments, 2)
	assert.Equal(t, "dev", loaded.Environments[0].Name)
	assert.True(t, loaded.Environments[0].IsDev)
	assert.Len(t, loaded.Services, 1)
	assert.Len(t, loaded.Services[0].Deployments, 2)

	dev := loaded.Services[0].Deployments[0]
	assert.Equal(t, "dev", dev.EnvName)
	assert.Equal(t, model.LocationLocal, dev.Location)
	assert.Equal(t, "go run .", dev.Command)

	prod := loaded.Services[0].Deployments[1]
	assert.Equal(t, "prod", prod.EnvName)
	assert.Equal(t, model.LocationRemote, prod.Location)
	assert.Equal(t, []string{"h-1"}, prod.HostIDs)
	assert.Equal(t, "systemctl start api-server", prod.StartCommand)
	assert.True(t, prod.ReadOnly)
}

func TestSaveAndReloadPreservesRuntimeAndProjectPipelines(t *testing.T) {
	dir := t.TempDir()
	p := model.Project{
		Name:      "tk",
		RootPath:  dir,
		Variables: map[string]string{"app_name": "tk"},
		Environments: []model.Environment{
			{Name: "dev", IsDev: true},
		},
		Services: []model.Service{{
			Name: "server",
			Deployments: []model.Deployment{{
				EnvName:  "dev",
				Location: model.LocationRemote,
				HostIDs:  []string{"dev-01"},
				Runtime: &model.RuntimeConfig{
					Type:        model.RuntimeTypeSystemd,
					ServiceName: "tk-dev",
					ReleaseDir:  "/opt/tk/releases",
					CurrentDir:  "/opt/tk/current",
					ExecStart:   "/opt/tk/current/tk",
				},
				Logs: &model.LogConfig{Type: model.LogKindJournalctl, Target: "tk-dev.service"},
			}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID:       "deploy-dev",
			Name:     "Deploy Dev",
			Services: []string{"server"},
			Roles: map[string]model.ProjectPipelineRole{
				"server_targets": {FromService: "server"},
			},
			Pipeline: model.Pipeline{Build: []model.Step{{Name: "Build", Type: "local_command"}}},
		}},
	}

	require.NoError(t, config.NewLoader(dir).Save(p))
	loaded, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	require.Len(t, loaded.Pipelines, 1)
	assert.Equal(t, "deploy-dev", loaded.Pipelines[0].ID)
	dep := loaded.Services[0].Deployments[0]
	require.NotNil(t, dep.Runtime)
	assert.Equal(t, model.RuntimeTypeSystemd, dep.Runtime.Type)
	require.NotNil(t, dep.Logs)
	assert.Equal(t, "tk-dev.service", dep.Logs.Target)
}

func TestLoadAndSaveProjectPipelineKindAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yaml := `
name: tk
environments:
  - name: prod
services:
  - name: api
    deployments:
      - env: prod
        location: remote
        hosts: [h1]
pipelines:
  - id: deploy-prod
    name: Deploy Prod
    services: [api]
    artifact_kind: file
    variables:
      artifact: "${artifacts}/api-${version}.tar.gz"
    pipeline:
      deploy:
        - name: Upload
          type: transfer
          concurrency: batch:2
          roles: [api_targets]
          with:
            source: "${artifact}"
            target: /opt/api/uploads/api.tar.gz
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yaml), 0o644))

	loader := config.NewLoader(dir)
	project, err := loader.Load()
	require.NoError(t, err)
	require.Len(t, project.Pipelines, 1)
	assert.Equal(t, model.ArtifactKindFile, project.Pipelines[0].ArtifactKind)
	assert.Equal(t, "${artifacts}/api-${version}.tar.gz", project.Pipelines[0].Variables["artifact"])
	assert.Equal(t, "batch:2", project.Pipelines[0].Pipeline.Deploy[0].Concurrency)

	require.NoError(t, loader.Save(project))
	data, err := os.ReadFile(filepath.Join(superdevDir, "config.yaml"))
	require.NoError(t, err)
	saved := string(data)
	assert.Contains(t, saved, "artifact_kind: file")
	assert.Contains(t, saved, "concurrency: batch:2")
	assert.NotContains(t, saved, "batch"+"_size:")
	assert.NotContains(t, saved, "tolerate"+"_failures:")
}

func TestLoadAndSavePreservesDeploymentWebEntrypoint(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yaml := `
name: webapp
environments:
  - name: dev
    is_dev: true
services:
  - name: web
    deployments:
      - id: dep-web-dev
        env: dev
        location: local
        web:
          enabled: true
          url: http://127.0.0.1:18991
          default_path: /
          readiness:
            type: http
            timeout_seconds: 5
          ai_debug:
            enabled: true
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yaml), 0o644))

	loader := config.NewLoader(dir)
	project, err := loader.Load()
	require.NoError(t, err)
	dep := project.Services[0].Deployments[0]
	require.NotNil(t, dep.Web)
	assert.Equal(t, "http://127.0.0.1:18991", dep.Web.URL)
	assert.Equal(t, "/", dep.Web.DefaultPath)
	assert.Equal(t, "http", dep.Web.Readiness.Type)
	assert.Equal(t, 5, dep.Web.Readiness.TimeoutSeconds)
	assert.True(t, dep.Web.AIDebug.Enabled)

	require.NoError(t, loader.Save(project))
	data, err := os.ReadFile(filepath.Join(superdevDir, "config.yaml"))
	require.NoError(t, err)
	saved := string(data)
	assert.Contains(t, saved, "web:")
	assert.Contains(t, saved, "ai_debug:")
	assert.Contains(t, saved, "timeout_seconds: 5")
}

func TestLoadAndSavePreservesDeploymentAutostartFields(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yaml := `
name: autostart-demo
environments:
  - name: dev
    is_dev: true
services:
  - id: server
    name: server
    deployments:
      - id: dep-server-dev
        env: dev
        location: local
        control_mode: managed
        start_on_boot: true
        depends_on:
          - database
        readiness:
          type: http
          target: http://127.0.0.1:18080/ready
          timeout_seconds: 12
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yaml), 0o644))

	loader := config.NewLoader(dir)
	project, err := loader.Load()
	require.NoError(t, err)
	dep := project.Services[0].Deployments[0]
	assert.True(t, dep.StartOnBoot)
	assert.Equal(t, []string{"database"}, dep.DependsOn)
	require.NotNil(t, dep.Readiness)
	assert.Equal(t, "http", dep.Readiness.Type)
	assert.Equal(t, "http://127.0.0.1:18080/ready", dep.Readiness.Target)
	assert.Equal(t, 12, dep.Readiness.TimeoutSeconds)

	require.NoError(t, loader.Save(project))
	reloaded, err := loader.Load()
	require.NoError(t, err)
	reloadedDep := reloaded.Services[0].Deployments[0]
	assert.True(t, reloadedDep.StartOnBoot)
	assert.Equal(t, []string{"database"}, reloadedDep.DependsOn)
	require.NotNil(t, reloadedDep.Readiness)
	assert.Equal(t, "http://127.0.0.1:18080/ready", reloadedDep.Readiness.Target)
}

func TestSaveAndReloadPreservesControlModeAndCustomLogCommands(t *testing.T) {
	dir := t.TempDir()
	p := model.Project{
		Name:     "tk",
		RootPath: dir,
		Environments: []model.Environment{
			{Name: "prod"},
		},
		Services: []model.Service{{
			Name: "server",
			Deployments: []model.Deployment{{
				EnvName:     "prod",
				Location:    model.LocationRemote,
				HostIDs:     []string{"prod-01"},
				ControlMode: model.ControlModeMonitor,
				Runtime: &model.RuntimeConfig{
					Type:        model.RuntimeTypeSystemd,
					ServiceName: "tk-prod.service",
				},
				Logs: &model.LogConfig{
					Type:    model.LogKindCommand,
					Command: "tail -F /var/log/tk/app.log",
				},
			}},
		}},
	}

	require.NoError(t, config.NewLoader(dir).Save(p))
	loaded, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	dep := loaded.Services[0].Deployments[0]
	assert.Equal(t, model.ControlModeMonitor, dep.ControlMode)
	assert.True(t, dep.IsReadOnly())
	require.NotNil(t, dep.Logs)
	assert.Equal(t, model.LogKindCommand, dep.Logs.Type)
	assert.Equal(t, "tail -F /var/log/tk/app.log", dep.Logs.Command)
}

func TestSaveAndReloadPreservesLaunchdRuntimeAndMacOSLog(t *testing.T) {
	dir := t.TempDir()
	p := model.Project{
		Name:     "tk",
		RootPath: dir,
		Environments: []model.Environment{
			{Name: "dev", IsDev: true},
		},
		Services: []model.Service{{
			Name: "agent",
			Deployments: []model.Deployment{{
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:      model.RuntimeTypeLaunchd,
					Label:     "com.example.api",
					PlistPath: "~/Library/LaunchAgents/com.example.api.plist",
				},
				Logs: &model.LogConfig{Type: model.LogKindMacOSLog, Target: "com.example.api"},
			}},
		}},
	}

	require.NoError(t, config.NewLoader(dir).Save(p))
	loaded, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	dep := loaded.Services[0].Deployments[0]
	require.NotNil(t, dep.Runtime)
	assert.Equal(t, model.RuntimeTypeLaunchd, dep.Runtime.Type)
	assert.Equal(t, "com.example.api", dep.Runtime.Label)
	assert.Equal(t, "~/Library/LaunchAgents/com.example.api.plist", dep.Runtime.PlistPath)
	require.NotNil(t, dep.Logs)
	assert.Equal(t, model.LogKindMacOSLog, dep.Logs.Type)
	assert.Equal(t, "com.example.api", dep.Logs.Target)
}

func TestSavePreservesLogRulesWithNewFormat(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	initialYaml := `
name: myapp
services: []
log_rules:
  - id: "r1"
    name: no ping
    type: exclude
    keywords: ["ping"]
    logic: or
    enabled: true
`
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(initialYaml), 0o644))

	p := model.Project{
		Name:     "myapp",
		RootPath: dir,
		Environments: []model.Environment{
			{Name: "dev", IsDev: true},
		},
	}
	require.NoError(t, loader.Save(p))

	rules, err := loader.LoadLogRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "no ping", rules[0].Name)
}

func TestLoadDebugCredentials(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(`
name: debugcred
debug_credentials:
  - name: test_login
    value: demo/demo123
    desc: login test account
services:
  - name: web
    debug_credentials:
      - name: api_key
        value: svc-key
        desc: service api key
`), 0o644))

	project, err := config.NewLoader(dir).Load()

	require.NoError(t, err)
	require.Len(t, project.DebugCredentials, 1)
	assert.Equal(t, "test_login", project.DebugCredentials[0].Name)
	assert.Equal(t, "demo/demo123", project.DebugCredentials[0].Value)
	require.Len(t, project.Services, 1)
	require.Len(t, project.Services[0].DebugCredentials, 1)
	assert.Equal(t, "api_key", project.Services[0].DebugCredentials[0].Name)
	assert.Equal(t, "svc-key", project.Services[0].DebugCredentials[0].Value)
}

func TestSaveAndReloadPreservesDebugCredentials(t *testing.T) {
	dir := t.TempDir()
	project := model.Project{
		Name:     "debugcred",
		RootPath: dir,
		DebugCredentials: []model.DebugCredential{
			{Name: "test_login", Value: "demo/demo123", Desc: "login test account"},
		},
		Services: []model.Service{{
			Name: "web",
			DebugCredentials: []model.DebugCredential{
				{Name: "api_key", Value: "svc-key", Desc: "service api key"},
			},
		}},
	}

	require.NoError(t, config.NewLoader(dir).Save(project))
	// 全新目录默认落 split 格式，主配置写入 project.yaml 而非 config.yaml；
	// debug_credentials 属共享层（用户裁决：刻意公开的测试凭据）。
	data, err := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "debug_credentials:")

	loaded, err := config.NewLoader(dir).Load()

	require.NoError(t, err)
	require.Len(t, loaded.DebugCredentials, 1)
	assert.Equal(t, "demo/demo123", loaded.DebugCredentials[0].Value)
	require.Len(t, loaded.Services, 1)
	require.Len(t, loaded.Services[0].DebugCredentials, 1)
	assert.Equal(t, "svc-key", loaded.Services[0].DebugCredentials[0].Value)
}

// writeConfig 把 yaml 内容写入 dir/.superdev/config.yaml。
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	sub := filepath.Join(dir, ".superdev")
	assert.NoError(t, os.MkdirAll(sub, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(sub, "config.yaml"), []byte(content), 0o644))
}

func TestSaveKeepsRelativePaths(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
name: demo
services:
  - name: api
    deployments:
      - env: dev
        location: local
        command: go run .
        working_dir: server
        env_file: server/.env
`)
	loader := config.NewLoader(dir)
	p, err := loader.Load()
	assert.NoError(t, err)
	// load 解析为绝对
	assert.Equal(t, filepath.Join(dir, "server"), p.Services[0].Deployments[0].WorkDir)
	assert.Equal(t, filepath.Join(dir, "server", ".env"), p.Services[0].Deployments[0].EnvFile)

	// save 后文件里必须仍是相对路径（修固化 bug）
	assert.NoError(t, loader.Save(p))
	raw, _ := os.ReadFile(filepath.Join(dir, ".superdev", "config.yaml"))
	assert.Contains(t, string(raw), "working_dir: server\n")
	assert.Contains(t, string(raw), "env_file: server/.env\n")
	assert.NotContains(t, string(raw), dir, "配置文件中不得出现项目根绝对路径")
}

func TestSaveRelativizesRuntimePaths(t *testing.T) {
	dir := t.TempDir()
	p := model.Project{
		Name: "demo", RootPath: dir,
		Services: []model.Service{{Name: "api", Deployments: []model.Deployment{{
			EnvName: "dev", Location: model.LocationLocal,
			WorkDir: filepath.Join(dir, "server"),
			Runtime: &model.RuntimeConfig{Type: model.RuntimeTypeLanguage,
				WorkingDir: filepath.Join(dir, "server"),
				EnvFile:    filepath.Join(dir, ".env"),
				CWD:        filepath.Join(dir, "server")},
		}}}},
	}
	loader := config.NewLoader(dir)
	assert.NoError(t, loader.Save(p))
	// 内存中的 Runtime 不得被 save 顺手改掉（拷贝而非原地相对化）
	assert.Equal(t, filepath.Join(dir, "server"), p.Services[0].Deployments[0].Runtime.WorkingDir)
	// 全新目录默认落 split 格式，主配置写入 project.yaml 而非 config.yaml。
	raw, err := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), dir)
}

// TestLoadResolvesRuntimePathsToAbsolute 是 TestSaveRelativizesRuntimePaths 的
// 对称另一半。save 侧把 Runtime 的三个路径字段相对化写盘，load 侧就必须把它们
// 解析回绝对——消费侧（codedebug 的 debugDeploymentWorkDir、handler_deployments）
// 是直接读 Runtime.WorkingDir / EffectiveCWD() 的，只补 dep.WorkDir 不补 Runtime
// 会让它们拿到相对路径，进而以 agent 自身工作目录为基准解析。
func TestLoadResolvesRuntimePathsToAbsolute(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
name: demo
services:
  - name: api
    deployments:
      - env: dev
        location: local
        runtime:
          type: language
          working_dir: server
          env_file: server/.env
          cwd: server/cmd
`)
	p, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	rt := p.Services[0].Deployments[0].Runtime
	require.NotNil(t, rt)
	assert.Equal(t, filepath.Join(dir, "server"), rt.WorkingDir)
	assert.Equal(t, filepath.Join(dir, "server", ".env"), rt.EnvFile)
	assert.Equal(t, filepath.Join(dir, "server", "cmd"), rt.CWD)
	assert.Equal(t, filepath.Join(dir, "server", "cmd"), rt.EffectiveCWD())

	// 解析回绝对之后再存回去仍须是相对，不能把绝对路径固化进配置文件。
	require.NoError(t, config.NewLoader(dir).Save(p))
	raw, err := os.ReadFile(filepath.Join(dir, ".superdev", "config.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), dir, "配置文件中不得出现项目根绝对路径")
	assert.Contains(t, string(raw), "cwd: server/cmd")
}

func TestLoadAINotesAndAuthHints(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(`
name: hinted
ai_note: project note
auth_hint: project auth
environments:
  - name: dev
    is_dev: true
    order: 0
    ai_note: env note
    auth_hint: env auth
services:
  - name: web
    ai_note: service note
    auth_hint: service auth
`), 0o644))

	project, err := config.NewLoader(dir).Load()

	require.NoError(t, err)
	assert.Equal(t, "project note", project.AINote)
	assert.Equal(t, "project auth", project.AuthHint)
	require.Len(t, project.Environments, 1)
	assert.Equal(t, "env note", project.Environments[0].AINote)
	assert.Equal(t, "env auth", project.Environments[0].AuthHint)
	require.Len(t, project.Services, 1)
	assert.Equal(t, "service note", project.Services[0].AINote)
	assert.Equal(t, "service auth", project.Services[0].AuthHint)
}

func TestSaveAndReloadPreservesAINotesAndAuthHints(t *testing.T) {
	dir := t.TempDir()
	project := model.Project{
		Name:     "hinted",
		RootPath: dir,
		AINote:   "project note",
		AuthHint: "project auth",
		Environments: []model.Environment{{
			Name: "dev", IsDev: true, AINote: "env note", AuthHint: "env auth",
		}},
		Services: []model.Service{{
			Name: "web", AINote: "service note", AuthHint: "service auth",
		}},
	}

	require.NoError(t, config.NewLoader(dir).Save(project))
	// 全新目录默认落 split 格式，主配置写入 project.yaml 而非 config.yaml。
	data, err := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "ai_note: project note")
	assert.Contains(t, string(data), "auth_hint: project auth")
	assert.Contains(t, string(data), "ai_note: env note")
	assert.Contains(t, string(data), "auth_hint: service auth")

	loaded, err := config.NewLoader(dir).Load()

	require.NoError(t, err)
	assert.Equal(t, "project note", loaded.AINote)
	assert.Equal(t, "project auth", loaded.AuthHint)
	require.Len(t, loaded.Environments, 1)
	assert.Equal(t, "env note", loaded.Environments[0].AINote)
	assert.Equal(t, "env auth", loaded.Environments[0].AuthHint)
	require.Len(t, loaded.Services, 1)
	assert.Equal(t, "service note", loaded.Services[0].AINote)
	assert.Equal(t, "service auth", loaded.Services[0].AuthHint)
}

// mustWriteSuperdev 写 dir/.superdev/<name>。
func mustWriteSuperdev(t *testing.T, dir, name, content string) {
	t.Helper()
	sub := filepath.Join(dir, ".superdev")
	assert.NoError(t, os.MkdirAll(sub, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(sub, name), []byte(content), 0o644))
}

func TestDetectFormat(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)
	assert.Equal(t, config.FormatSplit, loader.DetectFormat(), "全新目录默认新格式")

	writeConfig(t, dir, "name: demo\nservices: []\n")
	assert.Equal(t, config.FormatLegacy, loader.DetectFormat())

	// project.yaml 出现后优先于残留的 config.yaml
	assert.NoError(t, os.WriteFile(filepath.Join(dir, ".superdev", "project.yaml"),
		[]byte("name: demo\nservices: []\n"), 0o644))
	assert.Equal(t, config.FormatSplit, loader.DetectFormat())
}

func TestSplitLoadMergesLocal(t *testing.T) {
	dir := t.TempDir()
	mustWriteSuperdev(t, dir, "project.yaml", `
name: demo
services:
  - id: svc-1
    name: api
    deployments:
      - env: dev
        location: local
        working_dir: server
        env_vars: {PORT: "9100"}
`)
	mustWriteSuperdev(t, dir, "local.yaml", `
deployments:
  svc-1/dev:
    env_vars: {API_KEY: sk-live}
`)
	p, err := config.NewLoader(dir).Load()
	assert.NoError(t, err)
	assert.Equal(t, "split", p.ConfigFormat)
	dep := p.Services[0].Deployments[0]
	assert.Equal(t, "sk-live", dep.Env["API_KEY"])
	assert.Equal(t, "9100", dep.Env["PORT"])
}

func TestSplitSaveKeepsLayers(t *testing.T) {
	// 接 TestSplitLoadMergesLocal 的布局：load → 改共享键 → save
	dir := t.TempDir()
	mustWriteSuperdev(t, dir, "project.yaml", `
name: demo
services:
  - id: svc-1
    name: api
    deployments:
      - env: dev
        location: local
        env_vars: {PORT: "9100"}
`)
	mustWriteSuperdev(t, dir, "local.yaml", "deployments:\n  svc-1/dev:\n    env_vars: {API_KEY: sk-live}\n")
	loader := config.NewLoader(dir)
	p, _ := loader.Load()
	p.Services[0].Deployments[0].Env["PORT"] = "9200"
	assert.NoError(t, loader.Save(p))

	proj, _ := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	assert.Contains(t, string(proj), "PORT")
	assert.NotContains(t, string(proj), "API_KEY", "local 键不得泄漏进共享层")
	assert.NotContains(t, string(proj), "env_selected_service_ids", "UI 状态不再写 yaml")
	loc, _ := os.ReadFile(filepath.Join(dir, ".superdev", "local.yaml"))
	assert.Contains(t, string(loc), "API_KEY")
}

func TestSplitLogRules(t *testing.T) {
	dir := t.TempDir()
	mustWriteSuperdev(t, dir, "project.yaml", "name: demo\nservices: []\n")
	loader := config.NewLoader(dir)
	assert.NoError(t, loader.SaveLogRules([]model.LogRule{{ID: "r1"}}))
	rules, err := loader.LoadLogRules()
	assert.NoError(t, err)
	assert.Len(t, rules, 1)
	// log_rules 必须落在 project.yaml（共享层），不产生 config.yaml
	_, statErr := os.Stat(filepath.Join(dir, ".superdev", "config.yaml"))
	assert.True(t, os.IsNotExist(statErr))
}
