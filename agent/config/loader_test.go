package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/config"
	"github.com/superdev/agent/model"
)

func TestLoadProject(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yaml := `
name: myapp
services:
  - name: server
    command: go run .
    working_dir: ./server
    required: true
    order: 1
  - name: worker
    command: go run ./worker
    order: 2
selected_service_ids:
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
	assert.Equal(t, filepath.Join(dir, "server"), p.Services[0].WorkDir)
	assert.Equal(t, []string{"worker"}, p.SelectedServiceIDs)
	assert.Equal(t, model.StatusStopped, p.Services[0].Status)
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
			{Name: "api", Command: "go run .", WorkDir: ".", Order: 0},
		},
		SelectedServiceIDs: []string{"api"},
	}
	require.NoError(t, loader.Save(p))

	loaded, err := loader.Load()
	require.NoError(t, err)
	assert.Equal(t, "test", loaded.Name)
	assert.Equal(t, []string{"api"}, loaded.SelectedServiceIDs)
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
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yamlContent), 0o644))

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)

	prod := p.Services[0].Deployments[0]
	assert.True(t, prod.IsReadOnly())
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

func TestOldFormatMigratedToLocalDevDeployment(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yamlContent := `
name: myapp
services:
  - name: server
    command: go run .
    working_dir: ./server
    required: true
    order: 1
  - name: worker
    command: go run ./worker
    order: 2
selected_service_ids:
  - worker
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yamlContent), 0o644))

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)

	assert.Equal(t, "myapp", p.Name)
	assert.Len(t, p.Services, 2)
	assert.Equal(t, []string{"worker"}, p.SelectedServiceIDs)

	for _, svc := range p.Services {
		require.Len(t, svc.Deployments, 1, "service %q should have exactly 1 migrated deployment", svc.Name)
		dep := svc.Deployments[0]
		assert.Equal(t, model.LocationLocal, dep.Location)
		assert.Equal(t, "dev", dep.EnvName)
		assert.Equal(t, svc.Command, dep.Command)
		assert.False(t, dep.IsReadOnly())
	}

	serverDep := p.Services[0].Deployments[0]
	assert.Equal(t, filepath.Join(dir, "server"), serverDep.WorkDir)
}

func TestOldFormatWithExplicitDevEnv(t *testing.T) {
	dir := t.TempDir()
	superdevDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(superdevDir, 0o755))

	yamlContent := `
name: myapp
environments:
  - name: local
    is_dev: true
    order: 0
services:
  - name: api
    command: go run .
`
	require.NoError(t, os.WriteFile(filepath.Join(superdevDir, "config.yaml"), []byte(yamlContent), 0o644))

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)

	dep := p.Services[0].Deployments[0]
	assert.Equal(t, "local", dep.EnvName)
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
					},
				},
			},
		},
		SelectedServiceIDs: []string{"api-server"},
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
