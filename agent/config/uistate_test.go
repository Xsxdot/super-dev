package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUIStateStoreRoundTrip(t *testing.T) {
	s := NewUIStateStore(t.TempDir())
	assert.Empty(t, s.EnvSelected("/p1"), "无记录返回空")
	assert.NoError(t, s.SetEnvSelected("/p1", "dev", []string{"api", "worker"}))
	assert.NoError(t, s.SetEnvSelected("/p1", "prod", []string{"api"}))
	assert.Equal(t, []string{"api", "worker"}, s.EnvSelected("/p1")["dev"])
	assert.Equal(t, []string{"api"}, s.EnvSelected("/p1")["prod"])
	// 不同项目隔离
	assert.Empty(t, s.EnvSelected("/p2"))
	// Replace 整体覆盖
	assert.NoError(t, s.ReplaceEnvSelected("/p1", map[string][]string{"dev": {"x"}}))
	assert.Equal(t, map[string][]string{"dev": {"x"}}, s.EnvSelected("/p1"))
}
