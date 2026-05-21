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
