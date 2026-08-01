package config_test

import (
	"encoding/json"
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

// cleanLegacyFixture 是一份「没有任何要提醒的东西」的 legacy 配置：没有
// 疑似密钥键名/值前缀，没有 working_dir（因此内存态里没有绝对路径），
// .gitignore 已经是迁移后的目标状态。用来盯住 BuildMigrationPlan 在
// "全干净" 这一档结果下是否仍然正确——尤其是切片字段是否仍然是空数组
// 而不是 JSON 里的 null（nil 切片经 encoding/json 编码就是 "null"）。
const cleanLegacyFixture = `
name: clean
variables:
  PUBLIC_URL: http://localhost
services:
  - id: svc-1
    name: server
    deployments:
      - env: dev
        location: local
        command: go run ./cmd/server
        env_vars:
          PORT: "9100"
env_selected_service_ids:
  dev: [server]
`

func TestBuildMigrationPlanCleanProjectMarshalsEmptySlicesNotNull(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, cleanLegacyFixture)
	// .gitignore 已经是迁移后的目标状态：机器层/备份行都在，整目录忽略不在。
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), ".superdev/local.yaml\n.superdev/config.yaml.bak\nnode_modules/\n")

	plan, err := config.BuildMigrationPlan(dir)
	assert.NoError(t, err)

	// Go 值层面：确实什么都没扫到。
	assert.Empty(t, plan.Suspects)
	assert.Empty(t, plan.RelativizedPaths)
	assert.Empty(t, plan.Gitignore.RemoveLines)
	assert.Empty(t, plan.Gitignore.AddLines)

	// 只断言 Go 值 empty 不够——nil 切片和 make([]T, 0) 在 assert.Empty 下
	// 表现相同，但序列化结果天差地别（"null" vs "[]"），desktop 端对
	// suspects 之类字段做 .map(...) 会因为 null 直接崩。必须落到编码后的
	// JSON 字符串上验证。
	encoded, err := json.Marshal(plan)
	assert.NoError(t, err)
	body := string(encoded)
	assert.Contains(t, body, `"suspects":[]`)
	assert.Contains(t, body, `"relativized_paths":[]`)
	assert.Contains(t, body, `"remove_lines":[]`)
	assert.Contains(t, body, `"add_lines":[]`)
	assert.NotContains(t, body, "null")
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
