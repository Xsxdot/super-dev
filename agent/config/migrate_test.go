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

// legacyFixture 是一份典型的 legacy 单文件配置：有疑似密钥（变量与 env_vars
// 各一）、有固化的绝对 working_dir、有已迁走的 UI 状态、有 log_rules，也有
// 刻意公开的 debug_credentials。
//
// debug_credentials 必须用 model.DebugCredential 的真实 schema（name/value/
// desc）书写：yaml.v3 对结构体外的键静默丢弃，写成 account/password 这类不
// 存在的字段，值根本进不了内存，"凭据随共享层入库" 这条断言就会验在空气上。
const legacyFixture = `
name: tk
variables:
  PUBLIC_URL: http://localhost
  CLIENT_SECRET: real-secret-value
debug_credentials:
  - name: password
    value: Money8888
    desc: 测试账号密码
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

func TestApplyMigration(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, fmt.Sprintf(legacyFixture, filepath.Join(dir, "server")))
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), ".superdev/\nnode_modules/\n")
	uiStore := config.NewUIStateStore(t.TempDir())

	decisions := []config.MigrationDecision{
		{Scope: "variables", Key: "CLIENT_SECRET", Disposition: "local"},
		{Scope: "env_vars", Service: "server", Env: "dev", Key: "OPENAI_API_KEY", Disposition: "local"},
	}
	assert.NoError(t, config.ApplyMigration(dir, decisions, uiStore))

	// 1. project.yaml：相对路径、无密钥、无 UI 状态、保留 log_rules 与 debug_credentials
	proj, _ := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	s := string(proj)
	assert.Contains(t, s, "working_dir: server")
	assert.NotContains(t, s, dir)
	assert.NotContains(t, s, "real-secret-value")
	assert.NotContains(t, s, "sk-abc123def456")
	assert.NotContains(t, s, "env_selected_service_ids")
	assert.Contains(t, s, "log_rules")
	assert.Contains(t, s, "Money8888", "debug_credentials 留在共享层")

	// 2. local.yaml：密钥入本机层
	loc, _ := os.ReadFile(filepath.Join(dir, ".superdev", "local.yaml"))
	assert.Contains(t, string(loc), "real-secret-value")
	assert.Contains(t, string(loc), "sk-abc123def456")

	// 3. UI 状态入 store
	assert.Equal(t, []string{"server"}, uiStore.EnvSelected(dir)["dev"])

	// 4. 备份与 gitignore
	_, err := os.Stat(filepath.Join(dir, ".superdev", "config.yaml.bak"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, ".superdev", "config.yaml"))
	assert.True(t, os.IsNotExist(err))
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	assert.NotContains(t, string(gi), ".superdev/\n.superdev", "整目录忽略行已移除")
	assert.Contains(t, string(gi), ".superdev/local.yaml")
	assert.Contains(t, string(gi), "node_modules/", "无关行原样保留")

	// 5. 迁移后 Load 语义等价：合并态与迁移前一致
	p, err := config.NewLoader(dir).Load()
	assert.NoError(t, err)
	assert.Equal(t, "split", p.ConfigFormat)
	assert.Equal(t, "real-secret-value", p.Variables["CLIENT_SECRET"])
	assert.Equal(t, "sk-abc123def456", p.Services[0].Deployments[0].Env["OPENAI_API_KEY"])
	assert.Equal(t, "9100", p.Services[0].Deployments[0].Env["PORT"])

	// 6. 幂等：重跑报 ErrAlreadyMigrated
	assert.ErrorIs(t, config.ApplyMigration(dir, nil, uiStore), config.ErrAlreadyMigrated)
}

func TestApplyMigrationSharedDisposition(t *testing.T) {
	// 用户选择 CLIENT_SECRET 留共享层（不挡、只亮的另一半）
	dir := t.TempDir()
	writeConfig(t, dir, fmt.Sprintf(legacyFixture, "server"))
	uiStore := config.NewUIStateStore(t.TempDir())
	decisions := []config.MigrationDecision{
		{Scope: "variables", Key: "CLIENT_SECRET", Disposition: "shared"},
		{Scope: "env_vars", Service: "server", Env: "dev", Key: "OPENAI_API_KEY", Disposition: "local"},
	}
	assert.NoError(t, config.ApplyMigration(dir, decisions, uiStore))
	proj, _ := os.ReadFile(filepath.Join(dir, ".superdev", "project.yaml"))
	assert.Contains(t, string(proj), "real-secret-value", "用户明选 shared 则尊重")
}

// mustWriteFile 写入任意绝对路径文件（不局限于 .superdev/ 目录），仿
// loader_test.go 里的 mustWriteSuperdev，用于给迁移测试构造项目根
// .gitignore 等现场文件。
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
