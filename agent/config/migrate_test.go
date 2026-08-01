package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/config"
)

const legacyFixture = `
name: tk
variables:
  PUBLIC_URL: http://localhost
  CLIENT_SECRET: real-secret-value
debug_credentials:
  - account: admin
    password: Money8888
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        working_dir: %s
        env_vars:
          PORT: "9100"
          OPENAI_API_KEY: sk-abc123def456
env_selected_service_ids:
  dev: [server]
log_rules:
  - id: r1
`

func TestBuildMigrationPlan(t *testing.T) {
	dir := t.TempDir()
	// working_dir 用绝对路径构造固化现场
	writeConfig(t, dir, fmt.Sprintf(legacyFixture, filepath.Join(dir, "server")))
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), ".superdev/\nnode_modules/\n")

	plan, err := config.BuildMigrationPlan(dir)
	assert.NoError(t, err)
	assert.Equal(t, 1, plan.ServiceCount)
	assert.Equal(t, []string{"dev"}, plan.UIStateEnvs)
	assert.Len(t, plan.RelativizedPaths, 1, "绝对 working_dir 进相对化清单")

	// 疑似密钥：CLIENT_SECRET（键名命中）+ OPENAI_API_KEY（键名+sk- 值双命中）
	keys := map[string]bool{}
	for _, s := range plan.Suspects {
		keys[s.Key] = true
		assert.NotContains(t, s.Masked, "real-secret-value", "预览必须脱敏")
		assert.NotContains(t, s.Masked, "sk-abc123def456")
	}
	assert.True(t, keys["CLIENT_SECRET"])
	assert.True(t, keys["OPENAI_API_KEY"])
	assert.False(t, keys["PORT"], "普通变量不进疑似清单")
	assert.False(t, keys["password"], "debug_credentials 属共享层，不扫描（用户裁决）")

	// gitignore：撤掉整目录忽略，改为只忽略机器层与备份
	assert.Contains(t, plan.Gitignore.RemoveLines, ".superdev/")
	assert.Contains(t, plan.Gitignore.AddLines, ".superdev/local.yaml")
	assert.Contains(t, plan.Gitignore.AddLines, ".superdev/config.yaml.bak")
}

func TestBuildMigrationPlanAlreadySplit(t *testing.T) {
	dir := t.TempDir()
	mustWriteSuperdev(t, dir, "project.yaml", "name: demo\nservices: []\n")
	_, err := config.BuildMigrationPlan(dir)
	assert.ErrorIs(t, err, config.ErrAlreadyMigrated)
}

// mustWriteFile 写入任意绝对路径文件（不局限于 .superdev/ 目录），仿
// loader_test.go 里的 mustWriteSuperdev，用于给迁移测试构造项目根
// .gitignore 等现场文件。
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
