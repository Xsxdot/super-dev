package process_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/process"
)

func TestPIDStoreSetGet(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))

	ps.Set("dep-1", 12345)
	ps.Set("dep-2", 99999)
	require.NoError(t, ps.Flush())

	ps2 := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	pgids := ps2.LoadAll()
	assert.Equal(t, 12345, pgids["dep-1"])
	assert.Equal(t, 99999, pgids["dep-2"])
}

func TestPIDStoreRemove(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	ps.Set("dep-1", 12345)
	ps.Remove("dep-1")
	require.NoError(t, ps.Flush())

	ps2 := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	pgids := ps2.LoadAll()
	assert.Empty(t, pgids)
}

func TestPIDStoreKillAllClearsFile(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	ps.Set("dep-missing", 999999)
	require.NoError(t, ps.Flush())

	ps.KillAll()

	assert.Empty(t, ps.LoadAll())
}
