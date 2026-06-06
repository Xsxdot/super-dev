// managed_store_test.go 验证远端 managed deployment 清单持久化。
//
// 职责：
//   - 覆盖 managed-deployments.json 的缺失、读写和损坏 JSON 场景
//   - 确认写入结果是可读 JSON 文件而不是临时文件内容
//
// 边界：
//   - 不测试 collector reconcile 或 HTTP handler
package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestManagedStoreMissingFileReturnsEmptyList(t *testing.T) {
	store := NewManagedStore(t.TempDir())

	got, err := store.Load()

	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestManagedStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewManagedStore(dir)
	want := []model.ManagedDeployment{{
		DeploymentID: "dep-api-prod",
		ServiceID:    "svc-api",
		ServiceName:  "api",
		ProjectID:    "proj-prod",
		EnvName:      "prod",
		Location:     model.LocationLocal,
		Runtime:      &model.RuntimeConfig{Type: model.RuntimeTypeSystemd, ServiceName: "api.service"},
		Logs:         &model.LogConfig{Type: model.LogKindJournalctl, Target: "api.service"},
	}}

	require.NoError(t, store.Save(want))
	got, err := store.Load()

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, want[0].DeploymentID, got[0].DeploymentID)
	assert.Equal(t, model.LocationLocal, got[0].Location)
	raw, err := os.ReadFile(filepath.Join(dir, "managed-deployments.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "\n")
	assert.NotContains(t, string(raw), ".tmp")
}

func TestManagedStoreCorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "managed-deployments.json"), []byte("{broken"), 0o644))
	store := NewManagedStore(dir)

	_, err := store.Load()

	require.Error(t, err)
}
