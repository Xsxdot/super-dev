package config_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/config"
)

func TestRegistryAddAndList(t *testing.T) {
	reg := config.NewRegistry(filepath.Join(t.TempDir(), "projects.json"))

	require.NoError(t, reg.Add("/home/user/myapp"))
	require.NoError(t, reg.Add("/home/user/other"))

	paths := reg.List()
	assert.Contains(t, paths, "/home/user/myapp")
	assert.Contains(t, paths, "/home/user/other")
}

func TestRegistryNoDuplicates(t *testing.T) {
	reg := config.NewRegistry(filepath.Join(t.TempDir(), "projects.json"))
	require.NoError(t, reg.Add("/home/user/myapp"))
	require.NoError(t, reg.Add("/home/user/myapp"))
	assert.Len(t, reg.List(), 1)
}

func TestRegistryRemove(t *testing.T) {
	reg := config.NewRegistry(filepath.Join(t.TempDir(), "projects.json"))
	require.NoError(t, reg.Add("/home/user/myapp"))
	require.NoError(t, reg.Remove("/home/user/myapp"))
	assert.Empty(t, reg.List())
}

func TestRegistryPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	reg1 := config.NewRegistry(path)
	require.NoError(t, reg1.Add("/home/user/myapp"))

	reg2 := config.NewRegistry(path)
	assert.Contains(t, reg2.List(), "/home/user/myapp")
}

func TestRegistryMissingFile(t *testing.T) {
	reg := config.NewRegistry(filepath.Join(t.TempDir(), "nonexistent", "projects.json"))
	assert.Empty(t, reg.List())
	require.NoError(t, reg.Add("/home/user/myapp"))
	assert.Contains(t, reg.List(), "/home/user/myapp")
}
