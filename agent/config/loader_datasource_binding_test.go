package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestProjectYAMLRoundTripsDataSourceBinding(t *testing.T) {
	root := t.TempDir()
	writeProjectYAML(t, root, `
name: tk
services: []
data_source_binding:
  postgres:
    datasource_name: local-pg
    dev_database: tk_dev
    terminate_connections: true
  redis:
    datasource_name: local-redis
  max_concurrent_leases: 3
  default_ttl_minutes: 30
`)

	loader := config.NewLoader(root)
	p, err := loader.Load()
	require.NoError(t, err)
	require.NotNil(t, p.DataSourceBinding)
	require.NotNil(t, p.DataSourceBinding.Postgres)
	require.Equal(t, "tk_dev", p.DataSourceBinding.Postgres.DevDatabase)
	require.True(t, p.DataSourceBinding.Postgres.TerminateConnections)

	require.NoError(t, loader.Save(p))
	again, err := loader.Load()
	require.NoError(t, err)
	require.NotNil(t, again.DataSourceBinding)
	require.NotNil(t, again.DataSourceBinding.Redis)
	require.Equal(t, "local-redis", again.DataSourceBinding.Redis.DataSourceName)
	require.Equal(t, 3, again.DataSourceBinding.MaxConcurrentLeases)
	require.Equal(t, 30, again.DataSourceBinding.DefaultTTLMinutes)
}

func TestDataSourceBindingStaysInSharedLayerNotLocal(t *testing.T) {
	root := t.TempDir()
	loader := config.NewLoader(root)
	p := model.Project{Name: "tk", RootPath: root}
	p.DataSourceBinding = &dbprovision.ProjectBinding{
		Postgres: &dbprovision.PostgresBinding{
			DataSourceName:       "local-pg",
			DevDatabase:          "tk_dev",
			TerminateConnections: true,
		},
	}
	require.NoError(t, loader.Save(p))

	shared := readFile(t, filepath.Join(root, ".superdev", "project.yaml"))
	require.Contains(t, shared, "data_source_binding")
	localPath := filepath.Join(root, ".superdev", "local.yaml")
	if data, err := os.ReadFile(localPath); err == nil {
		require.NotContains(t, string(data), "data_source_binding")
	}
}

func writeProjectYAML(t *testing.T, root, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".superdev", "project.yaml"), []byte(content), 0o644))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}
